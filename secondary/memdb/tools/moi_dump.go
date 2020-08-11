package main

import (
	"strings"
	"bytes"
	"fmt"
	"encoding/json"

	"github.com/couchbase/indexing/secondary/stubs/nitro/mm"
	"github.com/couchbase/indexing/secondary/indexer"
	"github.com/couchbase/indexing/secondary/memdb"
)

func decode(bs []byte, buf []byte) ([]byte, []interface{}) {
	var revbuf []byte
	revbuf = append(revbuf, bs...)

	var err error

	if len(bs) > 120 {
		cdc := indexer.GetEncoder()
		_, err = cdc.ReverseCollate(revbuf, []bool{false, false, false, true, false, false})
		if err != nil {
			fmt.Println(err)
		}
		bs = revbuf
	}

	// Extract sec key and docid
	sie := indexer.ConvertToSIE(bs)

	buf, err = sie.ReadSecKey(buf[:0])
	if err != nil {
		panic(err)
	}
	// op := fmt.Sprintf("%s", buf)
	// fmt.Printf("seckey = %s\n", buf)

	// buf can be json Unmarshal into []interface{}
	var jsonSK []interface{}
	if err := json.Unmarshal(buf, &jsonSK); err != nil {
		panic(err)
	}

	if len(jsonSK) != 6 {
		return nil, nil
		// panic("not 6")
		// return true
	}

	buf, err = sie.ReadDocId(buf[:0])
	if err != nil {
		panic(err)
	}
	// fmt.Printf("docid = %s\n", buf)

	// return fmt.Sprintf("seckey(%s) docid(%s)", op, buf)

	// return false
	return buf, jsonSK
}

func stringItem(bs []byte, buf []byte) string {
	var revbuf []byte
	revbuf = append(revbuf, bs...)

	var err error

	if len(bs) > 120 {
		cdc := indexer.GetEncoder()
		_, err = cdc.ReverseCollate(revbuf, []bool{false, false, false, true, false, false})
		if err != nil {
			fmt.Println(err)
		}
		bs = revbuf
	}

	// Extract sec key and docid
	sie := indexer.ConvertToSIE(bs)

	buf, err = sie.ReadSecKey(buf[:0])
	if err != nil {
		panic(err)
	}
	op := fmt.Sprintf("%s", buf)
	// fmt.Printf("seckey = %s\n", buf)

	// buf can be json Unmarshal into []interface{}
	var jsonSK []interface{}
	if err := json.Unmarshal(buf, &jsonSK); err != nil {
		panic(err)
	}

	// if len(jsonSK) != 6 {
	// 	panic("not 6")
	// 	// return true
	// }

	buf, err = sie.ReadDocId(buf[:0])
	if err != nil {
		panic(err)
	}
	// fmt.Printf("docid = %s\n", buf)

	return fmt.Sprintf("seckey(%s) docid(%s)", op, buf)

	// return false
}

func check6(bs []byte, buf []byte) bool {
	var revbuf []byte
	revbuf = append(revbuf, bs...)

	var err error

	if len(bs) > 120 {
		cdc := indexer.GetEncoder()
		_, err = cdc.ReverseCollate(revbuf, []bool{false, false, false, true, false, false})
		if err != nil {
			fmt.Println(err)
		}
		bs = revbuf
	}

	// Extract sec key and docid
	sie := indexer.ConvertToSIE(bs)

	buf, err = sie.ReadSecKey(buf[:0])
	if err != nil {
		panic(err)
	}
	// fmt.Printf("seckey = %s\n", buf)

	// buf can be json Unmarshal into []interface{}
	var jsonSK []interface{}
	if err := json.Unmarshal(buf, &jsonSK); err != nil {
		panic(err)
	}

	if len(jsonSK) != 6 {
		// panic("NOT 6!!!!")
		return true
	}

	return false
}

func check(shardFilePath string) {
	// shardFilePath := "/Users/akhilmundroy13/akhilmd/tickets/CBSE-8684/shard-29-1gb"
	// shardFilePath := "/Users/akhilmundroy13/akhilmd/tickets/CBSE-8684/snapshot.2020-07-14.103121.468/data/shard-29"
	
	fmt.Println("moi dump", shardFilePath)


	cfg := memdb.DefaultConfig()
	cfg.UseMemoryMgmt(mm.Malloc, mm.Free)
	m := memdb.NewWithConfig(cfg)
	defer m.Close()

	version := 1

	reader := m.NewFileReader(memdb.RawdbFile, version)
	defer reader.Close()

	if err := reader.Open(shardFilePath); err != nil {
		panic(err)
	}

	n := 1000000000000000000
	// k := 0
	buf := make([]byte, 500000)
	set := make(map[string]int)
	var prevbs, bs []byte

	var runs []map[string]int

	i:= 0
	defer func () {
		fmt.Println(fmt.Sprintf("last item  = [%v]", stringItem(bs, buf)))
		fmt.Println("Checked", i, "items")
	}()

	for ;i<n;i++ {
		itm, err := reader.ReadItem()
		if err != nil {
			panic(err)
		}

		if itm == nil {
			break
		}

		bs = itm.Bytes()
		if i == 0 {
			fmt.Println(fmt.Sprintf("first item = [%v]", stringItem(bs, buf)))
		}
		// stringItem(bs, buf)

		// if check6(bs, buf) {
		// 	fmt.Println(fmt.Sprintf("i[%d] item repeat \nbs    [%v]", i, stringItem(bs, buf)))
		// }
		// continue

		// check for repeating items
		s := string(bs)
		if _, ok := set[s]; ok {
			// fmt.Println(fmt.Sprintf("oi[%d]i[%d] item repeat \nbs    [%v]\nprevbs[%v]", oi, i, stringItem(bs, buf), stringItem(prevbs, buf)))

			runs = append(runs, set)

			set = make(map[string]int)

			// panic("FFFF")

			// k++
			// if k >= 10 {
			// 	panic("Lots of repeats")
			// }
		}

		for si, oset := range runs {
			if _, ok := oset[s]; !ok {
				fmt.Printf("_____ [%d] does not have item[%s]\n", si, stringItem(bs, buf))
			}
		}

		set[s] = i

		// Check for non sorted
		if !(bytes.Compare(bs, prevbs) > 0) {
			fmt.Println(fmt.Sprintf("wrong order \nitem number[%d] item[%v]\nitem number[%d] item[%v]", i, stringItem(bs, buf), i-1, stringItem(prevbs, buf)))
			// panic("Wrong order")
		}

		prevbs = bs
	}
}

func dumpCSV(shardFilePath string) {
	cfg := memdb.DefaultConfig()
	cfg.UseMemoryMgmt(mm.Malloc, mm.Free)
	m := memdb.NewWithConfig(cfg)
	defer m.Close()

	version := 1

	reader := m.NewFileReader(memdb.RawdbFile, version)
	defer reader.Close()

	if err := reader.Open(shardFilePath); err != nil {
		panic(err)
	}

	n := 30000000000000000
	// k := 0
	buf := make([]byte, 500000)
	set := make(map[string]int)
	var bs []byte

	// var runs []map[string]int

	i:= 0
	defer func () {
		// fmt.Println(fmt.Sprintf("last item  = [%v]", stringItem(bs, buf)))
		// fmt.Println("Checked", i, "items")
	}()

	for ;i<n;i++ {
		itm, err := reader.ReadItem()
		if err != nil {
			panic(err)
		}

		if itm == nil {
			break
		}

		bs = itm.Bytes()

		s := string(bs)
		if _, ok := set[s]; ok {
			panic("REPEAT")
		}
		set[s] = i

		docid, secKeys := decode(bs, buf)

		if docid == nil {
			continue
		}

		var sks []string
		for _, itm := range secKeys {
			sitm, ok := itm.(string)
			if ok {
				sks = append(sks, fmt.Sprintf("%s", sitm))
			} else {
				iitm := int64(itm.(float64))
				sks = append(sks, fmt.Sprintf("%d", iitm))
			}
		}
		
		fmt.Printf("%s,%s\n", docid, strings.Join(sks, ","))
	}
}

func main() {
	// for i:=0;i<32;i++ {
	// 	// check(fmt.Sprintf("/Users/akhilmundroy13/akhilmd/tickets/CBSE-8684/snapshot.2020-07-14.103121.468/data/shard-%d", i))
	// 	dumpCSV(fmt.Sprintf("/Users/akhilmundroy13/akhilmd/tickets/CBSE-8684/snapshot.2020-07-14.103121.468/data/shard-%d", i))
	// }
	// check("/Users/akhilmundroy13/akhilmd/tickets/CBSE-8684/shard-29-1gb")
	// dumpCSV("/Users/akhilmundroy13/akhilmd/tickets/CBSE-8684/shard-29-1gb")
	check("/Users/akhilmundroy13/akhilmd/tickets/CBSE-8684/new-cluster/shard-31-1gb")
	// dumpCSV("/Users/akhilmundroy13/akhilmd/tickets/CBSE-8684/new-cluster/shard-31-1gb")
}