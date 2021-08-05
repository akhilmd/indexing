package randdocs

import (
	"crypto/rand"
	"encoding/json"
	"sync/atomic"
	"time"
)
import "crypto/md5"
import "fmt"
import "sync"
import "runtime"
import "github.com/couchbase/indexing/secondary/common"

type Config struct {
	ClusterAddr   string
	Bucket        string
	NumDocs       int
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
}

const PREFIX_LEN = 12

func randString(n int) string {
	//const alphanum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	const alphanum = "0123456789abcdef"
	var bytes = make([]byte, n)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = alphanum[b%byte(len(alphanum))]
	}
	return string(bytes)
}

func Run(cfg Config) error {
	fmt.Println("Run with cfg:")
	cfgBytes, err := json.MarshalIndent(cfg, "", "    ")
	fmt.Println(string(cfgBytes))

	runtime.GOMAXPROCS(cfg.Threads)

	b, err := common.ConnectBucket(cfg.ClusterAddr, "default", cfg.Bucket)
	if err != nil {
		return err
	}
	defer b.Close()

	var cnt int64
	fullStart := time.Now()

	opsPerSec := time.Duration(cfg.OpsPerSec / cfg.Threads)
	sleepPerOp := time.Second / opsPerSec
	fmt.Println("Sleep per op =", sleepPerOp)

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

					prefix := docid[cfg.DocIdLen-PREFIX_LEN:cfg.DocIdLen]
					suffix := randString(cfg.FieldSize)

					value := make(map[string]interface{})
					value["body"] = fmt.Sprintf("%s-%s", prefix, suffix)

					//value["body"] = randString(cfg.FieldSize)
					//if cfg.JunkFieldSize != 0 {
					//	value["junk"] = fmt.Sprintf("%0*d", cfg.JunkFieldSize, 0)
					//}

					//if cfg.ArrayLen > 0 {
					//	seed := rnd.Int() % 1000000
					//	val := []int{}
					//	for i := 0; i < cfg.ArrayLen; i++ {
					//		val = append(val, seed+i)
					//	}
					//	value["arr"] = val
					//}

					localErr := b.Set(docid, 0, value)
					if localErr != nil {
						fmt.Println(err)
						err = localErr
					}
					if k := atomic.AddInt64(&cnt, 1); k % 100000 == 0 {
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
		fmt.Println("Done setting docs:", cnt, "at", cnt/(1+int64(time.Since(fullStart).Seconds())))
	}

	return err
}
