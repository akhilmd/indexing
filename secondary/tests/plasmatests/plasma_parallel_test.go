//  Copyright (c) 2017 Couchbase, Inc.
//  Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
//  except in compliance with the License. You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
//  Unless required by applicable law or agreed to in writing, software distributed under the
//  License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
//  either express or implied. See the License for the specific language governing permissions
//  and limitations under the License.
package indexer

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"flag"
	"fmt"
	"hash/crc32"
	"math/rand"
	_ "net/http/pprof"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/couchbase/indexing/secondary/common"
	"github.com/couchbase/indexing/secondary/indexer"
	"github.com/couchbase/nitro/plasma"
)

// structure to maintain plasma and memDb datastructures which are used by different threads
type Context struct {
	plasmaDir   string
	plasmaSlice indexer.Slice
	plasmaSnaps []indexer.Snapshot
	plasmaInfos []indexer.SnapshotInfo

	mutationMetas []*indexer.MutationMeta
	keys          [][]byte
	docid         [][]byte
	numItems      int

	memDbDir   string
	memDbSlice indexer.Slice
	memDbSnaps []indexer.Snapshot
	memDbInfos []indexer.SnapshotInfo
}

// These are the input parameters
// eg : go test -v -test.run TestParallel -args -threads=1 -docs=50000
var threads = flag.Int("threads", 5, "a integer")
var docs = flag.Int("docs", 500000, "a integer")
var operations = flag.Int("operations", 5, "a integer")
var longevity_iterations = flag.Int("longevity_iterations", 5, "a integer")
var set_dgm = flag.Bool("set_dgm", false, "a boolean")
var set_aggressive_lss_fragmentation = flag.Bool("set_aggressive_lss_fragmentation", false, "a boolean")

func TestParallelOperations(t *testing.T) {
	var wg sync.WaitGroup
	if *set_dgm {
		plasma.SetMemoryQuota(1024 * 1024)
	}
	c := new(Context)
	// Set the path for memdb and plasma slices
	initalize1(c)
	// Create memdb and plasma slices
	createSlice1(c)
	// Generate data for testing
	genData1(c)
	for i := 0; i < *operations; i++ {
		fmt.Print("**** Iteration ", i, " started ****", "\n")
		for j := 0; j < *threads; j++ { // start multiple threads in parallel
			newMutationMeta1(c)
			wg.Add(1)
			// Do insert and delete operations
			go insertDelete1(c, c.mutationMetas[j], &wg)
		}
		// Wait for inserts and deletes to complete
		wg.Wait()
		// Print storage statistics for logging purposes
		storageStatistics1(c)
		// create 5 snapshots. In Plasma we store only 2 recent snapshots.
		// NewSnapshot() cannot be done in parallel with inserts and deletes
		for l := 0; l < 5; l++ {
			createSnapshot1(c)
		}
		for k := 0; k < *threads; k++ { // start multiple threads in parallel
			wg.Add(2)
			// Do writes and scan from snapshots
			// TODO: Delete some snapshots
			go insertDelete1(c, c.mutationMetas[k], &wg)
			// TODO: Add Range queries here
			go scan1(c, &wg)
		}
		// Wait for the all the above threads to complete
		wg.Wait()
		for k := 0; k < *threads; k++ {
			// Do rollback
			rollback1(c, &wg)
		}
		fmt.Print("**** Iteration ", i, " complete ****", "\n")
	}
	// Close all the snapshots, close all the slices and destroy the slices
	close1(c)
	memory := plasma.MemoryInUse()
	fmt.Print("\n MemoryInUse : ", memory, "\n")
	if memory != 0 {
		panic("MemoryInUse not 0 after the tests ended")
	}
	// Remove the memdb and plasma slice directories
	teardown1(c)
}

func TestLongevity(t *testing.T) {
	var wg sync.WaitGroup
	c := new(Context)
	if *set_dgm {
		plasma.SetMemoryQuota(1024 * 1024)
	}
	// Set the path for memdb and plasma slices
	initalize1(c)
	// Create memdb and plasma slices
	createSlice1(c)
	// Generate data for testing
	genData2(c)
	for i := 0; i < *longevity_iterations; i++ {
		fmt.Print("**** Iteration ", i, " started ****\n")
		for j := 0; j < *threads; j++ { // start multiple threads in parallel
			newMutationMeta1(c)
			wg.Add(1)
			// Do insert and delete operations
			go insertDelete1(c, c.mutationMetas[j], &wg)
		}
		// Wait for inserts and deletes to complete
		wg.Wait()
		// Print storage statistics for logging purposes
		storageStatistics1(c)
		// create 5 snapshots. In Plasma we store only 2 recent snapshots.
		// NewSnapshot() cannot be done in parallel with inserts and deletes
		for l := 0; l < 5; l++ {
			createSnapshot1(c)
		}
		for k := 0; k < *threads; k++ { // start multiple threads in parallel
			wg.Add(2)
			// Do writes and scan from snapshots
			// TODO: Delete some snapshots
			go insertDelete1(c, c.mutationMetas[k], &wg)
			// TODO: Add Range queries here
			go scan1(c, &wg)
		}
		// Wait for the all the above threads to complete
		wg.Wait()
		fmt.Print("**** Iteration ", i, " complete ****\n") // Ensure the script doesn't hang after 1st iteration here. Bug ?
	}
	for k := 0; k < *threads; k++ { // start multiple threads in parallel
		// Do rollback
		rollback1(c, &wg)
	}
	// Close all the snapshots, close all the slices and destroy the slices
	close1(c)
	memory := plasma.MemoryInUse()
	fmt.Print("\n MemoryInUse : ", memory, "\n")
	if memory != 0 {
		panic("MemoryInUse not 0 after the tests ended")
	}
	// Remove the memdb and plasma slice directories
	teardown1(c)
}

func TestAggressiveSplitAndMerge(t *testing.T) {
	var wg sync.WaitGroup
	if *set_dgm {
		plasma.SetMemoryQuota(1024 * 1024)
	}
	c := new(Context)
	// Set the path for memdb and plasma slices
	initalize1(c)
	// Create memdb and plasma slices
	createSlice1(c)
	// Generate data for testing
	genData3(c)
	for i := 0; i < *operations; i++ {
		fmt.Print("**** Iteration ", i, " started ****", "\n")
		// Do insert and delete operations
		for i := 0; i < 5; i++ {
			newMutationMeta1(c)
			wg.Add(1)
			go insertdata(c, &wg, c.mutationMetas[i], i*c.numItems/10, (i+1)*c.numItems/10)
		}
		wg.Wait()
		for i := 6; i < 10; i++ {
			wg.Add(2)
			go insertdata(c, &wg, c.mutationMetas[i-6], i*c.numItems/10, (i+1)*c.numItems/10)
			go deletedata(c, &wg, c.mutationMetas[i-6], (i-6)*c.numItems/10, (i-5)*c.numItems/10)
		}
		wg.Wait()
		// check storageStatistics for splits and merges
		storageStatistics, _ := c.memDbSlice.Statistics()
		fmt.Print("storageStatistics for memDb\n", storageStatistics, "\n")
		storageStatistics, _ = c.plasmaSlice.Statistics()
		fmt.Print("storageStatistics for plasma\n", storageStatistics, "\n")
		re := regexp.MustCompile(`splits            = (\d+)`)
		match := re.FindStringSubmatch(strings.Join(storageStatistics.InternalData, ""))
		splits, _ := strconv.Atoi(match[1])
		if splits == 0 {
			panic("Number of splits is 0")
		}
		re = regexp.MustCompile(`merges            = (\d+)`)
		match = re.FindStringSubmatch(strings.Join(storageStatistics.InternalData, ""))
		merges, _ := strconv.Atoi(match[1])
		if merges == 0 {
			panic("Number of merges is 0")
		}

		// create 5 snapshots. In Plasma we store only 2 recent snapshots.
		// NewSnapshot() cannot be done in parallel with inserts and deletes
		for l := 0; l < 5; l++ {
			createSnapshot1(c)
		}
		for k := 0; k < 5; k++ { // start multiple threads in parallel
			wg.Add(2)
			// Do writes and scan from snapshots
			// TODO: Delete some snapshots
			go insertDelete1(c, c.mutationMetas[k], &wg)
			// TODO: Add Range queries here
			go scan1(c, &wg)
		}
		// Wait for the all the above threads to complete
		wg.Wait()
		// Do rollback
		rollback1(c, &wg)
		fmt.Print("**** Iteration ", i, " complete ****", "\n")
	}
	// Close all the snapshots, close all the slices and destroy the slices
	close1(c)
	memory := plasma.MemoryInUse()
	fmt.Print("\n MemoryInUse : ", memory, "\n")
	if memory != 0 {
		panic("MemoryInUse not 0 after the tests ended")
	}
	// Remove the memdb and plasma slice directories
	teardown1(c)
}

func TestLongRunningScans(t *testing.T) {
	var wg sync.WaitGroup
	if *set_dgm {
		plasma.SetMemoryQuota(1024 * 1024)
	}
	c := new(Context)
	// Set the path for memdb and plasma slices
	initalize1(c)
	// Create memdb and plasma slices
	createSlice1(c)
	// Generate data for testing
	genData3(c)
	for i := 0; i < *operations; i++ {
		fmt.Print("**** Iteration ", i, " started ****", "\n")
		// Do insert and delete operations
		for i := 0; i < 5; i++ {
			newMutationMeta1(c)
			wg.Add(1)
			go insertdata(c, &wg, c.mutationMetas[i], i*c.numItems/10, (i+1)*c.numItems/10)
		}
		wg.Wait()
		for i := 6; i < 10; i++ {
			wg.Add(2)
			go insertdata(c, &wg, c.mutationMetas[i-6], i*c.numItems/10, (i+1)*c.numItems/10)
			go deletedata(c, &wg, c.mutationMetas[i-6], (i-6)*c.numItems/10, (i-5)*c.numItems/10)
		}
		wg.Wait()

		// check storageStatistics for splits and merges
		storageStatistics, _ := c.memDbSlice.Statistics()
		fmt.Print("storageStatistics for memDb\n", storageStatistics, "\n")
		storageStatistics, _ = c.plasmaSlice.Statistics()
		fmt.Print("storageStatistics for plasma\n", storageStatistics, "\n")
		re := regexp.MustCompile(`splits            = (\d+)`)
		match := re.FindStringSubmatch(strings.Join(storageStatistics.InternalData, ""))
		splits, _ := strconv.Atoi(match[1])
		if splits == 0 {
			panic("Number of splits is 0")
		}
		re = regexp.MustCompile(`merges            = (\d+)`)
		match = re.FindStringSubmatch(strings.Join(storageStatistics.InternalData, ""))
		merges, _ := strconv.Atoi(match[1])
		if merges == 0 {
			panic("Number of merges is 0")
		}

		// create 5 snapshots. In Plasma we store only 2 recent snapshots.
		// NewSnapshot() cannot be done in parallel with inserts and deletes
		for l := 0; l < 5; l++ {
			createSnapshot1(c)
		}
		for k := 0; k < 5; k++ { // start multiple threads in parallel
			wg.Add(2)
			// Do writes and scan from snapshots
			// TODO: Delete some snapshots
			go insertDelete1(c, c.mutationMetas[k], &wg)
			// TODO: Add Range queries here
			go scan1(c, &wg)
		}
		// Wait for the all the above threads to complete
		wg.Wait()
		// Do rollback
		rollback1(c, &wg)
		wg.Add(1)
		go scanAll(c, &wg)
		scanRange(c, &wg)
		wg.Wait()
		fmt.Print("**** Iteration ", i, " complete ****", "\n")
	}
	// Close all the snapshots, close all the slices and destroy the slices
	close1(c)
	memory := plasma.MemoryInUse()
	fmt.Print("\n MemoryInUse : ", memory, "\n")
	if memory != 0 {
		panic("MemoryInUse not 0 after the tests ended")
	}
	// Remove the memdb and plasma slice directories
	teardown1(c)
}

func TestGC(t *testing.T) {
	var wg sync.WaitGroup
	if *set_dgm {
		plasma.SetMemoryQuota(1024 * 1024)
	}
	c := new(Context)
	// Set the path for memdb and plasma slices
	initalize1(c)
	// Create memdb and plasma slices
	createSlice1(c)
	// Generate data for testing
	genData1(c)
	for j := 0; j < *operations; j++ { // start multiple threads in parallel
		newMutationMeta1(c)
	}
	var last_memory int64
	last_memory = 0
	for i := 0; i < *operations; i++ {
		wg.Add(1)
		// update fixed set of docs
		go insertdata(c, &wg, c.mutationMetas[i], i*c.numItems/10, (i+1)*c.numItems/10)
		wg.Wait()
		// create snapshot
		createSnapshot1(c)
		// open snapshot
		c.plasmaSnaps[i].Open()
		current_memory := plasma.MemoryInUse()
		fmt.Print("\n MemoryInUse iteration ", i+1, " : ", current_memory, "\n")
		if last_memory != 0 {
			ratio := float64(current_memory) / float64(last_memory)
			fmt.Print("Ratio : ", ratio, "\n")
		}
		last_memory = current_memory
	}

	for i := 0; i < *operations-1; i++ {
		// close all snapshots but the last one
		c.plasmaSnaps[i].Close()
	}

	last_snap := c.plasmaSnaps[*operations-1]
	last_memory = 0
	// delete all the old snaps from the context
	c.plasmaSnaps = c.plasmaSnaps[:0]
	for i := 0; i < *operations; i++ {
		wg.Add(1)
		// update fixed set of docs
		go insertdata(c, &wg, c.mutationMetas[i], i*c.numItems/10, (i+1)*c.numItems/10)
		wg.Wait()
		// create new snapshot
		createSnapshot1(c)
		c.plasmaSnaps[i].Open()
		// Close the last one
		last_snap.Close()
		last_snap = c.plasmaSnaps[i]
		current_memory := plasma.MemoryInUse()
		fmt.Print("\n MemoryInUse iteration ", i+1, " : ", current_memory, " \n")
		if last_memory != 0 {
			ratio := float64(current_memory) / float64(last_memory)
			fmt.Print("Ratio : ", ratio, "\n")
			if ratio > 1.3 {
				panic("Garbage collection doesn't seem to be happening..")
			}
		}
		last_memory = current_memory
	}
	// Close all the snapshots, close all the slices and destroy the slices
	close1(c)
	// Remove the memdb and plasma slice directories
	teardown1(c)
}

// Initialize the memdb and plasma directories
func initalize1(c *Context) {
	c.plasmaDir = "/tmp/plasma"
	c.memDbDir = "/tmp/memdb"
	err := os.RemoveAll(c.plasmaDir)
	if err != nil {
		fmt.Println(err)
	}
	err1 := os.RemoveAll(c.memDbDir)
	if err1 != nil {
		fmt.Println(err1)
	}
	c.numItems = *docs
	rand.Seed(time.Now().UnixNano())
}

// Create the slices
func createSlice1(c *Context) {
	config := common.SystemConfig.SectionConfig(
		"indexer.", true)
	config.SetValue("settings.moi.debug", true)
	if *set_aggressive_lss_fragmentation {
		config.SetValue("plasma.mainIndex.LSSFragmentation", 5)
		config.SetValue("plasma.backIndex.LSSFragmentation", 5)
		config.SetValue("plasma.mainIndex.maxLSSFragmentation", 5)
		config.SetValue("plasma.backIndex.maxLSSFragmentation", 5)
	}
	stats := &indexer.IndexStats{}
	stats.Init()

	idxDefn := common.IndexDefn{
		DefnId:          common.IndexDefnId(200),
		Name:            "plasma_slice_test",
		Using:           common.PlasmaDB,
		Bucket:          "default",
		IsPrimary:       false,
		SecExprs:        []string{"Testing"},
		ExprType:        common.N1QL,
		PartitionScheme: common.HASH,
		PartitionKey:    "Testing"}

	idxDefn1 := common.IndexDefn{
		DefnId:          common.IndexDefnId(200),
		Name:            "moi_slice_test",
		Using:           common.MemDB,
		Bucket:          "default",
		IsPrimary:       false,
		SecExprs:        []string{"Testing"},
		ExprType:        common.N1QL,
		PartitionScheme: common.HASH,
		PartitionKey:    "Testing"}

	instID, err1 := common.NewIndexInstId()
	common.CrashOnError(err1)
	slice1, err1 := indexer.NewMemDBSlice("/tmp/memdb", 0, idxDefn1, instID, false, true, config, stats)
	common.CrashOnError(err1)
	c.memDbSlice = slice1
	slice, err := indexer.NewPlasmaSlice("/tmp/plasma", 0, idxDefn, instID, false, config, stats)
	common.CrashOnError(err)
	c.plasmaSlice = slice
}

// Random data generator
func genData1(c *Context) {
	c.keys = make([][]byte, c.numItems)
	c.docid = make([][]byte, c.numItems)
	for i := 0; i < c.numItems; i++ {
		c.keys[i] = []byte(fmt.Sprint("keys%v", rand.Intn(10000000000)))
		c.docid[i] = []byte(fmt.Sprint("docid%v", rand.Intn(10000000000)))
	}
}

// Random data generator
func genData2(c *Context) {
	c.keys = make([][]byte, c.numItems)
	c.docid = make([][]byte, c.numItems)
	for i := 0; i < c.numItems; i++ {
		hasher := md5.New()
		hasher.Write([]byte(fmt.Sprint("keys%v", rand.Intn(10000000000))))
		c.keys[i] = []byte(strings.Repeat(hex.EncodeToString(hasher.Sum(nil)), rand.Intn(10)+1))
		hasher = md5.New()
		hasher.Write([]byte(fmt.Sprint("docid%v", rand.Intn(10000000000))))
		c.docid[i] = []byte(strings.Repeat(hex.EncodeToString(hasher.Sum(nil)), rand.Intn(10)+1))
	}
}

// Random data generator for split/merge aggressive workload
func genData3(c *Context) {
	keys1 := make([]string, c.numItems)
	docid1 := make([]string, c.numItems)
	for i := 0; i < c.numItems; i++ {
		keys1[i] = string(fmt.Sprint("keys%v", rand.Intn(10000000000)))
		docid1[i] = string(fmt.Sprint("docid%v", rand.Intn(10000000000)))
	}
	sort.Strings(keys1)
	sort.Strings(docid1)
	c.keys = make([][]byte, c.numItems)
	c.docid = make([][]byte, c.numItems)
	for i := 0; i < c.numItems; i++ {
		c.keys[i] = []byte(keys1[i])
		c.docid[i] = []byte(docid1[i])
	}
}

// NewMutationMeta returns meta information for a KV Mutation
func newMutationMeta1(c *Context) {
	c.mutationMetas = append(c.mutationMetas, indexer.NewMutationMeta())
}

// Function to do inserts and random deletes
// TODO: Add support for read/write ratio
func insertDelete1(c *Context, mutationMeta *indexer.MutationMeta, wg *sync.WaitGroup) {
	defer wg.Done()
	for i := 0; i < c.numItems; i++ {
		mutationMeta.SetVBId(int(crc32.ChecksumIEEE(c.docid[i])) % 1024)
		c.memDbSlice.Insert(c.keys[i], c.docid[i], mutationMeta)
		c.plasmaSlice.Insert(c.keys[i], c.docid[i], mutationMeta)
	}

	arr := randomGenerator(c)
	index := rand.Intn(len(arr) - 2)
	// TODO: Generate 2 random numbers and use them as a range to delete
	for i := arr[index]; i < arr[index+1]; i++ {
		mutationMeta.SetVBId(int(crc32.ChecksumIEEE(c.docid[i])) % 1024)
		c.memDbSlice.Delete(c.docid[i], mutationMeta)
		c.plasmaSlice.Delete(c.docid[i], mutationMeta)
	}
}

func insertdata(c *Context, wg *sync.WaitGroup, mutationMeta *indexer.MutationMeta, start int, end int) {
	defer wg.Done()
	for i := start; i < end; i++ {
		mutationMeta.SetVBId(int(crc32.ChecksumIEEE(c.docid[i])) % 1024)
		c.memDbSlice.Insert(c.keys[i], c.docid[i], mutationMeta)
		c.plasmaSlice.Insert(c.keys[i], c.docid[i], mutationMeta)
	}
}

func deletedata(c *Context, wg *sync.WaitGroup, mutationMeta *indexer.MutationMeta, start int, end int) {
	defer wg.Done()
	for i := start; i < end; i++ {
		mutationMeta.SetVBId(int(crc32.ChecksumIEEE(c.docid[i])) % 1024)
		c.memDbSlice.Delete(c.docid[i], mutationMeta)
		c.plasmaSlice.Delete(c.docid[i], mutationMeta)
	}
}

// Printing storage statistics for logging purposes only
func storageStatistics1(c *Context) {
	storageStatistics, _ := c.memDbSlice.Statistics()
	fmt.Print("Statistics for MOI \n")
	fmt.Print(storageStatistics)
	fmt.Print("\n\n")
	storageStatistics, _ = c.plasmaSlice.Statistics()
	fmt.Print("Statistics for Plasma \n")
	fmt.Print(storageStatistics)
	fmt.Print("\n\n")
}

// Create snapshot, Open snapshot and Get snapshots
func createSnapshot1(c *Context) {
	ts := common.NewTsVbuuid("default", 1024)
	for i := uint64(1); i < uint64(1024); i++ {
		ts.Seqnos[i] = uint64(1000000 + i)
		ts.Vbuuids[i] = uint64(2000000 + i)
		ts.Snapshots[i] = [2]uint64{i, i + 10}
	}

	// Create snapshots
	info, err := c.memDbSlice.NewSnapshot(ts, true)
	common.CrashOnError(err)

	info1, err1 := c.plasmaSlice.NewSnapshot(ts, true)
	common.CrashOnError(err1)

	// Open snapshot
	snap, err5 := c.memDbSlice.OpenSnapshot(info)
	common.CrashOnError(err5)
	c.memDbSnaps = append(c.memDbSnaps, snap)

	snap, err5 = c.plasmaSlice.OpenSnapshot(info1)
	common.CrashOnError(err5)
	c.plasmaSnaps = append(c.plasmaSnaps, snap)

	time.Sleep(time.Second * 60)

	// Get snapshots, This is used is rollback operations
	infos, err6 := c.memDbSlice.GetSnapshots()
	common.CrashOnError(err6)
	c.memDbInfos = infos

	infos1, err7 := c.plasmaSlice.GetSnapshots()
	common.CrashOnError(err7)
	c.plasmaInfos = infos1
}

// Do both memdb and plasma scans for checking validity
// NOTE: Currently MOI is used a for comparison with Plasma to check for data correctness
// TODO: Add Range queries
func scan1(c *Context, wg *sync.WaitGroup) {
	defer wg.Done()
	time.Sleep(time.Minute)
	var index int

	reader := c.memDbSlice.GetReaderContext()
	reader1 := c.plasmaSlice.GetReaderContext()
	reader1.Init()
	scanReqMemDb := new(indexer.ScanRequest)
	scanReqMemDb.Ctx = reader
	stopchMemDb := make(indexer.StopChannel)
	scanReqPlasma := new(indexer.ScanRequest)
	scanReqPlasma.Ctx = reader1
	stopchPlasma := make(indexer.StopChannel)

	if len(c.memDbSnaps) > 1 && len(c.plasmaSnaps) > 1 {
		index = rand.Intn(len(c.memDbSnaps) - 1)
	} else {
		fmt.Errorf("Number of snaps on MemDb and Plasma are not same")
	}

	if scanReqMemDb.Low == nil && scanReqMemDb.High == nil && scanReqPlasma.Low == nil && scanReqPlasma.High == nil {
		var statsCountTotal, statsCountTotal1 uint64
		var err1, err2 error

		// Get different stats and compare MOI with plasma numbers
		statsCountTotal, err1 = c.memDbSnaps[index].StatCountTotal()
		common.CrashOnError(err1)
		statsCountTotal1, err2 = c.plasmaSnaps[index].StatCountTotal()
		common.CrashOnError(err2)

		if statsCountTotal != statsCountTotal1 {
			fmt.Print("\n Mismatch statsCountTotal : MemDb ", statsCountTotal, " and Plasma : ", statsCountTotal1, "\n")
		} else {
			fmt.Print("\n statsCountTotal : MemDb ", statsCountTotal, " and Plasma : ", statsCountTotal1, "\n")
		}

	} else {
		var countRange, countRange1 uint64
		var err1, err2 error

		countRange, err1 = c.memDbSnaps[index].CountRange(scanReqMemDb.Ctx, scanReqMemDb.Low, scanReqMemDb.High, scanReqMemDb.Incl, stopchMemDb)
		common.CrashOnError(err1)
		countRange1, err2 = c.plasmaSnaps[index].CountRange(scanReqPlasma.Ctx, scanReqPlasma.Low, scanReqPlasma.High, scanReqPlasma.Incl, stopchPlasma)
		common.CrashOnError(err2)
		if countRange != countRange1 {
			fmt.Print("\n Mismatch countRange : MemDb ", countRange, " and Plasma : ", countRange1, "\n")
		} else {
			fmt.Print("\n countRange : MemDb ", countRange, " and Plasma : ", countRange1, "\n")
		}
	}

	var countTotal, countTotal1 uint64
	var err1, err2 error

	countTotal, err1 = c.memDbSnaps[index].CountTotal(scanReqMemDb.Ctx, stopchMemDb)
	common.CrashOnError(err1)
	countTotal1, err2 = c.plasmaSnaps[index].CountTotal(scanReqPlasma.Ctx, stopchPlasma)
	common.CrashOnError(err2)
	if countTotal != countTotal1 {
		fmt.Print("\n Mismatch countTotal : MemDb ", countTotal, " and Plasma : ", countTotal1, "\n")
	} else {
		fmt.Print("\n countTotal : MemDb ", countTotal, " and Plasma : ", countTotal1, "\n")
	}

	var countLookup, countLookup1 uint64
	countLookup, err1 = c.memDbSnaps[index].CountLookup(scanReqMemDb.Ctx, scanReqMemDb.Keys, stopchMemDb)
	common.CrashOnError(err1)
	countLookup1, err2 = c.plasmaSnaps[index].CountLookup(scanReqPlasma.Ctx, scanReqPlasma.Keys, stopchPlasma)
	common.CrashOnError(err2)
	if countLookup != countLookup1 {
		fmt.Print("\n Mismatch countLookup : MemDb ", countLookup, " and Plasma : ", countLookup1, "\n")
	} else {
		fmt.Print("\n countLookup : MemDb ", countLookup, " and Plasma : ", countLookup1, "\n")
	}

}

func scanAll(c *Context, wg *sync.WaitGroup) {
	defer wg.Done()
	var index int

	reader := c.memDbSlice.GetReaderContext()
	reader1 := c.plasmaSlice.GetReaderContext()
	reader1.Init()
	scanReqMemDb := new(indexer.ScanRequest)
	scanReqMemDb.Ctx = reader
	scanReqPlasma := new(indexer.ScanRequest)
	scanReqPlasma.Ctx = reader1

	if len(c.memDbSnaps) > 1 && len(c.plasmaSnaps) > 1 {
		index = rand.Intn(len(c.memDbSnaps) - 1)
	} else {
		fmt.Errorf("Number of snaps on MemDb and Plasma are not same")
	}

	var count, count1 uint64
	memDbData := make([][]byte, c.numItems)
	plasmaData := make([][]byte, c.numItems)

	callb := func(current []byte) error {
		select {
		default:
			memDbData = append(memDbData, current)
			count++
		}
		return nil
	}
	callb1 := func(current []byte) error {
		select {
		default:
			plasmaData = append(plasmaData, current)
			count1++
		}
		return nil
	}
	err1 := c.memDbSnaps[index].All(scanReqMemDb.Ctx, callb)
	common.CrashOnError(err1)
	err2 := c.plasmaSnaps[index].All(scanReqPlasma.Ctx, callb1)
	common.CrashOnError(err2)
	if count != count1 {
		fmt.Print("\ncount : ", count)
		fmt.Print("\ncount1 : ", count1)
		panic("scanAll count of memDB and plasma doesnt't match")
	}

	if len(memDbData) >= len(plasmaData) {
		for i := range memDbData {
			if !bytes.Equal(memDbData[i], plasmaData[i]) {
				fmt.Print("\n memDbData : ", memDbData[i], " plasmaData : ", plasmaData[i])
			}
		}
	} else {
		for i := range plasmaData {
			if !bytes.Equal(memDbData[i], plasmaData[i]) {
				fmt.Print("\n memDbData : ", memDbData[i], " plasmaData : ", plasmaData[i])
			}
		}
	}
}

func scanRange(c *Context, wg *sync.WaitGroup) {
	// defer wg.Done()
	var index int

	reader := c.memDbSlice.GetReaderContext()
	reader1 := c.plasmaSlice.GetReaderContext()
	reader1.Init()
	scanReqMemDb := new(indexer.ScanRequest)
	scanReqMemDb.Ctx = reader
	scanReqPlasma := new(indexer.ScanRequest)
	scanReqPlasma.Ctx = reader1

	if len(c.memDbSnaps) > 1 && len(c.plasmaSnaps) > 1 {
		index = rand.Intn(len(c.memDbSnaps) - 1)
	} else {
		fmt.Errorf("Number of snaps on MemDb and Plasma are not same")
	}

	var count, count1 uint64
	memDbData := make([][]byte, c.numItems)
	plasmaData := make([][]byte, c.numItems)

	callb := func(current []byte) error {
		select {
		default:
			memDbData = append(memDbData, current)
			count++
		}
		return nil
	}
	callb1 := func(current []byte) error {
		select {
		default:
			plasmaData = append(plasmaData, current)
			count1++
		}
		return nil
	}
	low, _ := indexer.NewSecondaryKey([]byte("a"), make([]byte, 5000))
	high, _ := indexer.NewSecondaryKey([]byte("z"), make([]byte, 5000))
	err2 := c.plasmaSnaps[index].Range(scanReqPlasma.Ctx, low, high, indexer.Both, callb1)
	common.CrashOnError(err2)
	err1 := c.memDbSnaps[index].Range(scanReqMemDb.Ctx, low, high, indexer.Both, callb)
	common.CrashOnError(err1)

	fmt.Print("\ncount : ", count)
	fmt.Print("\ncount1 : ", count1)
	if count != count1 {
		fmt.Print("\ncount : ", count)
		fmt.Print("\ncount1 : ", count1)
		panic("scanRange count of memDB and plasma doesnt't match")
	}

	if len(memDbData) >= len(plasmaData) {
		for i := range memDbData {
			if !bytes.Equal(memDbData[i], plasmaData[i]) {
				fmt.Print("\n memDbData : ", memDbData[i], " plasmaData : ", plasmaData[i])
			}
		}
	} else {
		for i := range plasmaData {
			if !bytes.Equal(memDbData[i], plasmaData[i]) {
				fmt.Print("\n memDbData : ", memDbData[i], " plasmaData : ", plasmaData[i])
			}
		}
	}

}

// Function to do a rollback
func rollback1(c *Context, wg *sync.WaitGroup) {
	var index int

	if len(c.memDbInfos) > 1 && len(c.plasmaInfos) > 1 {
		index = rand.Intn(len(c.memDbInfos) - 1)
	} else {
		fmt.Errorf("Number of snapshotInfos on MemDb and Plasma are not same")
	}

	// Rollback slice to given snapshot
	err8 := c.memDbSlice.Rollback(c.memDbInfos[index])
	common.CrashOnError(err8)
	err8 = c.plasmaSlice.Rollback(c.plasmaInfos[index])
	common.CrashOnError(err8)

	//  Rollbacks the slice to initial state
	err9 := c.memDbSlice.RollbackToZero()
	common.CrashOnError(err9)
	err9 = c.plasmaSlice.RollbackToZero()
	common.CrashOnError(err9)
}

// Cleanup function to close snapshots , clsose and destroy slices
func close1(c *Context) {
	for _, snap := range c.memDbSnaps {
		snap.Close()
	}
	for _, snap := range c.plasmaSnaps {
		snap.Close()
	}
	c.memDbSlice.Close()
	c.memDbSlice.Destroy()
	c.plasmaSlice.Close()
	c.plasmaSlice.Destroy()
}

// Teardown function to remove the plasma and memdb slice directories
func teardown1(c *Context) {
	err := os.RemoveAll(c.plasmaDir)
	if err != nil {
		fmt.Println(err)
		return
	}
	err = os.RemoveAll(c.memDbDir)
	if err != nil {
		fmt.Println(err)
		return
	}
}

func randomGenerator(c *Context) (arr []int) {
	var rand_arr []int
	for i := 0; i <= 102; i++ {
		rand_arr = append(rand_arr, rand.Intn(c.numItems))
	}
	sort.Ints(rand_arr)
	return rand_arr
}
