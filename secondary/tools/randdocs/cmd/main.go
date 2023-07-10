package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/couchbase/indexing/secondary/tools/randdocs"
	"io/ioutil"
	"os"
)

func main() {

	help := flag.Bool("help", false, "Help")
	config := flag.String("config", "config.json", "Config file")
	Threads := flag.Int("Threads", -1, "Number of threads")
	NumDocs := flag.Int("NumDocs", -1, "Number of docs")
	DocIdLen := flag.Int("DocIdLen", -1, "Length of docid")
	UseRandDocID := flag.Bool("UseRandDocID", false, "Use Random docid")
	FieldSize := flag.Int("FieldSize", -1, "Field size will be at least this much")
	OpsPerSec := flag.Int("OpsPerSec", -1, "How many ops per sec")
	Iterations := flag.Int("Iterations", -1, "How many times to repeat")
	HotColdWorkLoad := flag.Bool("HotColdWorkLoad", false, "Do hot-cold workload?")
	HotDocWorkLoad := flag.Bool("HotDocWorkLoad", false, "Do hot workload on docs?")
	HotSizePerc := flag.Int("HotSizePerc", -1, "Percentage of index to be hot - from the beginnig")
	HotMutPerc := flag.Int("HotMutPerc", -1, "Percentage of mutations to be hot")
	Duration := flag.Int("Duration", -1, "Duration instead of iterations")

	flag.Parse()
	if *help {
		flag.PrintDefaults()
		os.Exit(0)
	}

	if *config == "" {
		fmt.Println("Config is empty!")
		flag.PrintDefaults()
		return
	}

	bs, err := ioutil.ReadFile(*config)
	if err != nil {
		fmt.Printf("Error occured: %v\n", err)
		os.Exit(1)
	}

	var cfg randdocs.Config
	if err := json.Unmarshal(bs, &cfg); err != nil {
		fmt.Printf("Error occured: %v\n", err)
		os.Exit(1)
	}

	if *Threads != -1 {
		cfg.Threads = *Threads
	}

	if *NumDocs != -1 {
		cfg.NumDocs = *NumDocs
	}

	if *UseRandDocID {
		cfg.UseRandDocID = *UseRandDocID
	}

	if *FieldSize != -1 {
		cfg.FieldSize = *FieldSize
	}

	if *DocIdLen != -1 {
		cfg.DocIdLen = *DocIdLen
	}

	if *OpsPerSec != -1 {
		cfg.OpsPerSec = *OpsPerSec
	}

	if *Iterations != -1 {
		cfg.Iterations = *Iterations
	}

	if *HotColdWorkLoad {
		cfg.HotColdWorkload = *HotColdWorkLoad
	}

	if *HotDocWorkLoad {
		cfg.HotDocWorkload = *HotDocWorkLoad
	}

	if *HotSizePerc != -1 {
		cfg.HotSizePerc = uint64(*HotSizePerc)
	}

	if *HotMutPerc != -1 {
		cfg.HotMutPerc = *HotMutPerc
	}

	if *Duration != -1 {
		cfg.Duration = *Duration
	}

	err = randdocs.Run(cfg)
	if err != nil {
		fmt.Println("randdocs err:", err)
	}
}
