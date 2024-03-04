package randdocs

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	rnd "math/rand"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/couchbase/indexing/secondary/common"
)

type Config struct {
	ClusterAddr string
	Bucket      string
	NumDocs     int

	Ops        int
	StatForOps string

	DocIdLen      int
	FieldSize     int
	ArrayLen      int
	JunkFieldSize int
	Iterations    int
	Threads       int
	DocNumOffset  int
	OpsPerSec     int

	// Use 16 byte random docid
	UseRandDocID bool

	HotColdWorkload bool
	HotDocWorkload  bool

	HotSizePerc uint64
	HotMutPerc  int

	Duration int
}

const (
	PREFIX_LEN = 12
	ALPHANUM   = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

func randFromAlphabet(n int, alphabet string) string {
	var bytes = make([]byte, n)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = alphabet[b%byte(len(alphabet))]
	}
	return string(bytes)
}

func randString(n int) string {
	return randFromAlphabet(n, ALPHANUM)
}

func Run(cfg Config) error {
	rndr := rnd.New(rnd.NewSource(time.Now().UnixNano()))

	cfgBytes, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return err
	}
	fmt.Printf("randdocs: Runing with cfg: \n%s\n", string(cfgBytes))

	runtime.GOMAXPROCS(cfg.Threads)

	b, err := common.ConnectBucket(cfg.ClusterAddr, "default", cfg.Bucket)
	if err != nil {
		return err
	}
	defer b.Close()

	host, _, err := net.SplitHostPort(cfg.ClusterAddr)
	if err != nil {
		return err
	}
	indexerAddr := fmt.Sprintf("%s:%s", host, "9102")

	prevVal := int64(-1)
	shouldProceed := func() (ppp bool) {
		client := &http.Client{}
		address := "http://" + indexerAddr + "/stats?async=false"

		req, _ := http.NewRequest("GET", address, nil)
		req.SetBasicAuth("Administrator", "asdasd")
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		resp, err := client.Do(req)

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
			fmt.Printf(address)
			fmt.Printf("%v", req)
			fmt.Printf("%v", resp)
			fmt.Printf("Get stats failed\n")
		}

		if err != nil {
			fmt.Println("Failed to get stats from indexer! err =", err)
			return false
		}

		defer resp.Body.Close()

		response := make(map[string]interface{})
		body, _ := ioutil.ReadAll(resp.Body)
		err = json.Unmarshal(body, &response)
		if err != nil {
			fmt.Println("Failed to parse stats from indexer! err =", err)
			return false
		}

		val := int64(response[cfg.StatForOps].(float64))
		fmt.Println("got val =", val)
		if pVal := atomic.LoadInt64(&prevVal); pVal == -1 {
			fmt.Println("Starting with val =", val)
			atomic.StoreInt64(&prevVal, val)
			return true
		} else {
			diff := val - prevVal
			if diff < 0 {
				fmt.Println("Failed! stat has reduced!")
				return false
			} else if diff > int64(cfg.Ops) {
				fmt.Println("Stat has reached amt ", val, diff, cfg.Ops)
				return false
			}
		}

		return true
	}

	cachedShouldProceed := int64(1)
	shouldProceed2 := func() bool {
		if atomic.LoadInt64(&cachedShouldProceed) == 1 {
			if !shouldProceed() {
				atomic.StoreInt64(&cachedShouldProceed, 0)
				return false
			}

		} else {
			return false
		}

		return true
	}

	var cnt, ttries, tetries int64
	fullStart := time.Now()

	opsPerSec := time.Duration(cfg.OpsPerSec / cfg.Threads)
	sleepPerOp := time.Second / opsPerSec
	fmt.Printf("randdocs: Sleep per op = [%v]\n", sleepPerOp)

	maxHotPrefix := strings.Repeat("F", PREFIX_LEN)
	mhpBase10Big := new(big.Int)
	mhpBase10Big.SetString(maxHotPrefix, 16)

	mhpBase10 := mhpBase10Big.Uint64()

	fmt.Println("Hot Size Perc :", cfg.HotSizePerc)
	fmt.Println("Hot Mut Perc :", cfg.HotMutPerc)
	hptBase10 := (cfg.HotSizePerc * mhpBase10) / 100

	hptBase10Big := new(big.Int)
	hptBase10Big.SetUint64(hptBase10)

	hotPrefixThreshold := hptBase10Big.Text(16)
	hotPrefixThreshold = strings.Repeat("0", PREFIX_LEN-len(hotPrefixThreshold)) + hotPrefixThreshold
	fmt.Printf("For [%d%%] hot data, threshold is [%s]\n", cfg.HotSizePerc, hotPrefixThreshold)

	hotColdWorkload := cfg.HotColdWorkload
	hotDocWorkload := cfg.HotDocWorkload

	mutStart := time.Now()
	durr := time.Duration(cfg.Duration) * time.Minute
	if hotColdWorkload {
		fmt.Println("HotColdWorkload")

		for itr := 0; itr < cfg.Iterations; itr++ {

			var wg sync.WaitGroup

			for thr := 0; thr < cfg.Threads; thr++ {

				wg.Add(1)
				go func(offset int) {

					fmt.Printf("new thread offset[%d] num[%d]\n", offset, offset+cfg.NumDocs/cfg.Threads)
					defer wg.Done()

					for i := 0; i < cfg.NumDocs/cfg.Threads; i++ {
						start := time.Now()
						doHot := rndr.Intn(100) < cfg.HotMutPerc
						tries := 0

					retry:
						tries++
						roff := rndr.Intn(cfg.NumDocs / cfg.Threads)
						docidSeq := fmt.Sprintf("%0*d", cfg.DocIdLen, roff+offset+cfg.DocNumOffset)[:cfg.DocIdLen]

						docid := docidSeq
						if cfg.UseRandDocID {
							key := md5.Sum([]byte(docid))
							docid = fmt.Sprintf("%x", key)[:cfg.DocIdLen]
						}

						prefix := docid[cfg.DocIdLen-PREFIX_LEN : cfg.DocIdLen]

						gotHot := bytes.Compare([]byte(prefix), []byte(hotPrefixThreshold)) < 0

						if gotHot != doHot {
							goto retry
						}

						// got the type we want!!

						suffix := randFromAlphabet(cfg.FieldSize, docid)

						value := make(map[string]interface{})
						value["body"] = fmt.Sprintf("%s-%s", prefix, suffix)

						etries := 0
					errRetry:
						etries++

						localErr := b.Set(docid, 0, value)
						if localErr != nil {
							fmt.Println(localErr)
							time.Sleep(250 * time.Microsecond)
							goto errRetry
						}

						// op is done without error

						m := atomic.AddInt64(&ttries, int64(tries))
						em := atomic.AddInt64(&tetries, int64(etries))
						if k := atomic.AddInt64(&cnt, 1); k%100000 == 0 {
							fmt.Printf("Set %7d docs at %dops/sec with %.1ftries/op %.1fetries/op\n", k, k/(1+int64(time.Since(fullStart).Seconds())), float64(m)/float64(k), float64(em)/float64(k))
						}

						dur := time.Since(start)
						toSleep := sleepPerOp - dur
						if toSleep > 0 {
							time.Sleep(toSleep)
						}

						if durr > 0 && time.Since(mutStart) > durr {
							return
						}
					}
				}(thr * cfg.NumDocs / cfg.Threads)
			}
			wg.Wait()

			fmt.Printf("Done setting [%d] docs at [%v]sets/sec and itr [%d]\n", cnt, cnt/(1+int64(time.Since(fullStart).Seconds())), itr)

			if durr > 0 && time.Since(mutStart) > durr {
				break
			}
		}
	} else if hotDocWorkload {
		fmt.Println("HotDocWorkload")

		for itr := 0; itr < cfg.Iterations; itr++ {

			var wg sync.WaitGroup

			for thr := 0; thr < cfg.Threads; thr++ {

				wg.Add(1)
				go func(offset, id int, rndrT *rnd.Rand) {
					if !shouldProceed2() {
						return
					}

					fmt.Printf("new thread offset[%d] num[%d]\n", offset, cfg.Ops/cfg.Threads)
					defer wg.Done()

					for i := 0; i < (10 * cfg.Ops / cfg.Threads); i++ {
						start := time.Now()
						doHot := rndrT.Intn(100) < cfg.HotMutPerc
						tries := 0

					retry:
						tries++

						roff := 0
						if doHot {
							mx := (cfg.NumDocs * int(cfg.HotSizePerc)) / 100
							l := mx / cfg.Threads
							o := id * l
							roff = o + rndrT.Intn(l)
						} else {
							panic("bruh")
							roff = rndrT.Intn(cfg.NumDocs)
							gotHot := roff < ((cfg.NumDocs * int(cfg.HotSizePerc)) / 100)
							if gotHot != doHot {
								goto retry
							}
						}

						// got the type we want!!

						docidSeq := fmt.Sprintf("%0*d", cfg.DocIdLen, roff)[:cfg.DocIdLen]

						docid := docidSeq
						if cfg.UseRandDocID {
							key := md5.Sum([]byte(docid))
							docid = fmt.Sprintf("%x", key)[:cfg.DocIdLen]
						}

						prefix := docid[cfg.DocIdLen-PREFIX_LEN : cfg.DocIdLen]
						suffix := randFromAlphabet(cfg.FieldSize, docid)

						value := make(map[string]interface{})
						value["body"] = fmt.Sprintf("%s-%s", prefix, suffix)

						etries := 0
					errRetry:
						etries++

						localErr := b.Set(docid, 0, value)
						if localErr != nil {
							time.Sleep(250 * time.Microsecond)
							goto errRetry
						}

						// op is done without error

						m := atomic.AddInt64(&ttries, int64(tries))
						em := atomic.AddInt64(&tetries, int64(etries))
						if k := atomic.AddInt64(&cnt, 1); k%100000 == 0 {
							fmt.Printf("Set %7d docs at %dops/sec with %.1ftries/op %.1fetries/op\n", k, k/(1+int64(time.Since(fullStart).Seconds())), float64(m)/float64(k), float64(em)/float64(k))
							if !shouldProceed2() {
								fmt.Println("done reaching diff!")
								return
							}
						}

						if atomic.LoadInt64(&cachedShouldProceed) == 0 {
							fmt.Println("cac done reaching diff!")
							return
						}

						dur := time.Since(start)
						toSleep := sleepPerOp - dur
						if toSleep > 0 {
							time.Sleep(toSleep)
						}

						if durr > 0 && time.Since(mutStart) > durr {
							fmt.Println("Done due to durr", durr)
							return
						}
					}
				}(thr*cfg.NumDocs/cfg.Threads, thr, rnd.New(rnd.NewSource(time.Now().UnixNano())))
			}
			wg.Wait()

			fmt.Printf("Done setting [%d] docs at [%v]sets/sec and itr [%d]\n", cnt, cnt/(1+int64(time.Since(fullStart).Seconds())), itr)

			if durr > 0 && time.Since(mutStart) > durr {
				break
			}
		}
	} else {
		fmt.Println("Full Random")

		for itr := 0; itr < cfg.Iterations; itr++ {

			var wg sync.WaitGroup
			for thr := 0; thr < cfg.Threads; thr++ {

				wg.Add(1)
				go func(offset int) {
					defer wg.Done()

					for i := 0; i < cfg.NumDocs/cfg.Threads; i++ {
						start := time.Now()
						docid := fmt.Sprintf("%0*d", cfg.DocIdLen, i+offset+cfg.DocNumOffset)[:cfg.DocIdLen]

						if cfg.UseRandDocID {
							key := md5.Sum([]byte(docid))
							docid = fmt.Sprintf("%x", key)[:cfg.DocIdLen]
						}

						prefix := docid[cfg.DocIdLen-PREFIX_LEN : cfg.DocIdLen]
						suffix := randFromAlphabet(cfg.FieldSize, docid)

						value := make(map[string]interface{})
						value["body"] = fmt.Sprintf("%s-%s", prefix, suffix)

						if cfg.JunkFieldSize != 0 {
							value["field"] = randString(cfg.FieldSize)
							value["junk"] = fmt.Sprintf("%0*d", cfg.JunkFieldSize, 0)
						}

						if cfg.ArrayLen > 0 {
							seed := rndr.Int() % 1000000
							val := []int{}
							for i := 0; i < cfg.ArrayLen; i++ {
								val = append(val, seed+i)
							}
							value["arr"] = val
						}

						localErr := b.Set(docid, 0, value)
						if localErr != nil {
							fmt.Println(err)
							err = localErr
						}

						if k := atomic.AddInt64(&cnt, 1); k%100000 == 0 {
							fmt.Printf("Set %7d docs at %dops/sec\n", k, k/(1+int64(time.Since(fullStart).Seconds())))
						}

						dur := time.Since(start)
						toSleep := sleepPerOp - dur
						if toSleep > 0 {
							time.Sleep(toSleep)
						}
					}
				}(thr * cfg.NumDocs / cfg.Threads)
			}
			wg.Wait()

			fmt.Printf("Done setting [%d] docs at [%v]sets/sec and itr [%d]\n", cnt, cnt/(1+int64(time.Since(fullStart).Seconds())), itr)
		}
	}

	return err
}
