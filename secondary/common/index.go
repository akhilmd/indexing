// Copyright (c) 2014 Couchbase, Inc.
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
// except in compliance with the License. You may obtain a copy of the License at
//   http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed under the
// License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
// either express or implied. See the License for the specific language governing permissions
// and limitations under the License.

package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/couchbase/indexing/secondary/logging"
	"strings"
)

type IndexKey []byte

type IndexerId string

const INDEXER_ID_NIL = IndexerId("")

// SecondaryKey is secondary-key in the shape of - [ val1, val2, ..., valN ]
// where value can be any golang data-type that can be serialized into JSON.
// simple-key shall be shaped as [ val ]
type SecondaryKey []interface{}

type Unbounded int

const (
	MinUnbounded Unbounded = -1
	MaxUnbounded           = 1
)

// IndexStatistics captures statistics for a range or a single key.
type IndexStatistics interface {
	Count() (int64, error)
	MinKey() (SecondaryKey, error)
	MaxKey() (SecondaryKey, error)
	DistinctCount() (int64, error)
	Bins() ([]IndexStatistics, error)
}

type IndexDefnId uint64
type IndexInstId uint64

type ExprType string

const (
	JavaScript ExprType = "JavaScript"
	N1QL                = "N1QL"
)

type PartitionScheme string

const (
	KEY    PartitionScheme = "KEY"
	HASH                   = "HASH"
	RANGE                  = "RANGE"
	TEST                   = "TEST"
	SINGLE                 = "SINGLE"
)

type HashScheme int

const (
	CRC32 HashScheme = iota
)

func (s HashScheme) String() string {

	switch s {
	case CRC32:
		return "CRC32"
	}

	return "HASH_SCHEME_UNKNOWN"
}

type IndexState int

const (
	//Create Index Processed
	INDEX_STATE_CREATED IndexState = iota
	// Index is stream is ready
	INDEX_STATE_READY
	//Initial Build In Progress
	INDEX_STATE_INITIAL
	//Catchup In Progress
	INDEX_STATE_CATCHUP
	//Maitenance Stream
	INDEX_STATE_ACTIVE
	//Drop Index Processed
	INDEX_STATE_DELETED
	//Error State: not a persistent state -- but used in function return value
	INDEX_STATE_ERROR
	// Nil State (used for no-op / invalid) -- not a persistent state
	INDEX_STATE_NIL
)

func (s IndexState) String() string {

	switch s {
	case INDEX_STATE_CREATED:
		return "INDEX_STATE_CREATED"
	case INDEX_STATE_READY:
		return "INDEX_STATE_READY"
	case INDEX_STATE_INITIAL:
		return "INDEX_STATE_INITIAL"
	case INDEX_STATE_CATCHUP:
		return "INDEX_STATE_CATCHUP"
	case INDEX_STATE_ACTIVE:
		return "INDEX_STATE_ACTIVE"
	case INDEX_STATE_DELETED:
		return "INDEX_STATE_DELETED"
	case INDEX_STATE_ERROR:
		return "INDEX_STATE_ERROR"
	default:
		return "INDEX_STATE_UNKNOWN"
	}
}

type IndexerState int

const (
	//Active(processing mutation and scan)
	INDEXER_ACTIVE IndexerState = iota
	//Paused(not processing mutation/scan)
	INDEXER_PAUSED
	INDEXER_PREPARE_UNPAUSE
	//Initial Bootstrap
	INDEXER_BOOTSTRAP
)

func (s IndexerState) String() string {

	switch s {
	case INDEXER_ACTIVE:
		return "Active"
	case INDEXER_PAUSED:
		return "Paused"
	case INDEXER_PREPARE_UNPAUSE:
		return "PrepareUnpause"
	case INDEXER_BOOTSTRAP:
		return "Warmup"
	default:
		return "Invalid"
	}
}

// Consistency definition for index-scan queries.
type Consistency byte

const (
	// AnyConsistency indexer would return the most current
	// data available at the moment.
	AnyConsistency Consistency = iota + 1

	// SessionConsistency indexer would query the latest timestamp
	// from each KV node. It will ensure that the scan result is at
	// least as recent as the KV timestamp. In other words, this
	// option ensures the query result is at least as recent as what
	// the user session has observed so far.
	SessionConsistency

	// QueryConsistency indexer would accept a timestamp vector,
	// and make sure to return a stable data-set that is atleast as
	// recent as the timestamp-vector.
	QueryConsistency
)

func (cons Consistency) String() string {
	switch cons {
	case AnyConsistency:
		return "ANY_CONSISTENCY"
	case SessionConsistency:
		return "SESSION_CONSISTENCY"
	case QueryConsistency:
		return "QUERY_CONSISTENCY"
	default:
		return "UNKNOWN_CONSISTENCY"
	}
}

//IndexDefn represents the index definition as specified
//during CREATE INDEX
type IndexDefn struct {
	// Index Definition
	DefnId          IndexDefnId     `json:"defnId,omitempty"`
	Name            string          `json:"name,omitempty"`
	Using           IndexType       `json:"using,omitempty"`
	Bucket          string          `json:"bucket,omitempty"`
	BucketUUID      string          `json:"bucketUUID,omitempty"`
	IsPrimary       bool            `json:"isPrimary,omitempty"`
	SecExprs        []string        `json:"secExprs,omitempty"`
	ExprType        ExprType        `json:"exprType,omitempty"`
	PartitionScheme PartitionScheme `json:"partitionScheme,omitempty"`
	//PartitionKey is obsolete
	PartitionKey       string     `json:"partitionKey,omitempty"`
	WhereExpr          string     `json:"where,omitempty"`
	Desc               []bool     `json:"desc,omitempty"`
	Deferred           bool       `json:"deferred,omitempty"`
	Immutable          bool       `json:"immutable,omitempty"`
	Nodes              []string   `json:"nodes,omitempty"`
	IsArrayIndex       bool       `json:"isArrayIndex,omitempty"`
	TTL                uint32     `json:"ttl,omitempty"`
	NumReplica         uint32     `json:"numReplica,omitempty"`
	PartitionKeys      []string   `json:"partitionKeys,omitempty"`
	RetainDeletedXATTR bool       `json:"retainDeletedXATTR,omitempty"`
	HashScheme         HashScheme `json:"hashScheme,omitempty"`
	NumReplica2        Counter    `json:"NumReplica2,omitempty"`

	// Sizing info
	NumDoc        uint64  `json:"numDoc,omitempty"`
	SecKeySize    uint64  `json:"secKeySize,omitempty"`
	DocKeySize    uint64  `json:"docKeySize,omitempty"`
	ArrSize       uint64  `json:"arrSize,omitempty"`
	ResidentRatio float64 `json:"residentRatio,omitempty"`

	// transient field (not part of index metadata)
	// These fields are used for create index during DDL, rebalance, or restore
	InstVersion   int           `json:"instanceVersion,omitempty"`
	ReplicaId     int           `json:"replicaId,omitempty"`
	InstId        IndexInstId   `json:"instanceId,omitempty"`
	Partitions    []PartitionId `json:"partitions,omitempty"`
	Versions      []int         `json:"versions,omitempty"`
	NumPartitions uint32        `json:"numPartitions,omitempty"`
	RealInstId    IndexInstId   `json:"realInstId,omitempty"`
}

//IndexInst is an instance of an Index(aka replica)
type IndexInst struct {
	InstId         IndexInstId
	Defn           IndexDefn
	State          IndexState
	RState         RebalanceState
	Stream         StreamId
	Pc             PartitionContainer
	Error          string
	BuildTs        []uint64
	Version        int
	ReplicaId      int
	Scheduled      bool
	StorageMode    string
	OldStorageMode string
	RealInstId     IndexInstId
}

//IndexInstMap is a map from IndexInstanceId to IndexInstance
type IndexInstMap map[IndexInstId]IndexInst

func (idx IndexDefn) String() string {

	str := fmt.Sprintf("DefnId: %v ", idx.DefnId)
	str += fmt.Sprintf("Name: %v ", idx.Name)
	str += fmt.Sprintf("Using: %v ", idx.Using)
	str += fmt.Sprintf("Bucket: %v ", idx.Bucket)
	str += fmt.Sprintf("IsPrimary: %v ", idx.IsPrimary)
	str += fmt.Sprintf("NumReplica: %v ", idx.GetNumReplica())
	str += fmt.Sprintf("TTL: %v ", idx.TTL)
	str += fmt.Sprintf("InstVersion: %v ", idx.InstVersion)
	str += fmt.Sprintf("\n\t\tSecExprs: %v ", logging.TagUD(idx.SecExprs))
	str += fmt.Sprintf("\n\t\tDesc: %v", idx.Desc)
	str += fmt.Sprintf("\n\t\tPartitionScheme: %v ", idx.PartitionScheme)
	str += fmt.Sprintf("\n\t\tHashScheme: %v ", idx.HashScheme.String())
	str += fmt.Sprintf("PartitionKeys: %v ", idx.PartitionKeys)
	str += fmt.Sprintf("WhereExpr: %v ", logging.TagUD(idx.WhereExpr))
	str += fmt.Sprintf("RetainDeletedXATTR: %v ", idx.RetainDeletedXATTR)
	return str

}

// This function makes a copy of index definition, excluding any transient
// field.  It is a shallow copy (e.g. does not clone field 'Nodes').
func (idx IndexDefn) Clone() *IndexDefn {
	return &IndexDefn{
		DefnId:             idx.DefnId,
		Name:               idx.Name,
		Using:              idx.Using,
		Bucket:             idx.Bucket,
		BucketUUID:         idx.BucketUUID,
		IsPrimary:          idx.IsPrimary,
		SecExprs:           idx.SecExprs,
		Desc:               idx.Desc,
		ExprType:           idx.ExprType,
		PartitionScheme:    idx.PartitionScheme,
		PartitionKeys:      idx.PartitionKeys,
		HashScheme:         idx.HashScheme,
		WhereExpr:          idx.WhereExpr,
		Deferred:           idx.Deferred,
		Immutable:          idx.Immutable,
		Nodes:              idx.Nodes,
		IsArrayIndex:       idx.IsArrayIndex,
		TTL:                idx.TTL,
		NumReplica:         idx.NumReplica,
		RetainDeletedXATTR: idx.RetainDeletedXATTR,
		NumDoc:             idx.NumDoc,
		SecKeySize:         idx.SecKeySize,
		DocKeySize:         idx.DocKeySize,
		ArrSize:            idx.ArrSize,
		NumReplica2:        idx.NumReplica2,
	}
}

func (idx *IndexDefn) HasDescending() bool {

	if idx.Desc != nil {
		for _, d := range idx.Desc {
			if d {
				return true
			}
		}
	}
	return false

}

func (idx *IndexDefn) GetNumReplica() int {

	numReplica, hasValue := idx.NumReplica2.Value()
	if !hasValue {
		return int(idx.NumReplica)
	}

	return int(numReplica)
}

func (idx IndexInst) IsProxy() bool {
	return idx.RealInstId != 0
}

func (idx IndexInst) String() string {

	str := "\n"
	str += fmt.Sprintf("\tInstId: %v\n", idx.InstId)
	str += fmt.Sprintf("\tDefn: %v\n", idx.Defn)
	str += fmt.Sprintf("\tState: %v\n", idx.State)
	str += fmt.Sprintf("\tRState: %v\n", idx.RState)
	str += fmt.Sprintf("\tStream: %v\n", idx.Stream)
	str += fmt.Sprintf("\tVersion: %v\n", idx.Version)
	str += fmt.Sprintf("\tReplicaId: %v\n", idx.ReplicaId)
	str += fmt.Sprintf("\tPartitionContainer: %v", idx.Pc)
	return str

}

func (idx IndexInst) DisplayName() string {

	return FormatIndexInstDisplayName(idx.Defn.Name, idx.ReplicaId)
}

func FormatIndexInstDisplayName(name string, replicaId int) string {

	return FormatIndexPartnDisplayName(name, replicaId, 0, false)
}

func FormatIndexPartnDisplayName(name string, replicaId int, partitionId int, isPartition bool) string {

	if isPartition {
		name = fmt.Sprintf("%v %v", name, partitionId)
	}

	if replicaId != 0 {
		return fmt.Sprintf("%v (replica %v)", name, replicaId)
	}

	return name
}

//StreamId represents the possible mutation streams
type StreamId uint16

const (
	NIL_STREAM StreamId = iota
	MAINT_STREAM
	CATCHUP_STREAM
	INIT_STREAM
	ALL_STREAMS
)

func (s StreamId) String() string {

	switch s {
	case MAINT_STREAM:
		return "MAINT_STREAM"
	case CATCHUP_STREAM:
		return "CATCHUP_STREAM"
	case INIT_STREAM:
		return "INIT_STREAM"
	case NIL_STREAM:
		return "NIL_STREAM"
	default:
		return "INVALID_STREAM"
	}
}

type RebalanceState int

const (
	REBAL_ACTIVE RebalanceState = iota
	REBAL_PENDING
	REBAL_NIL
	REBAL_MERGED
	REBAL_PENDING_DELETE
)

func (s RebalanceState) String() string {

	switch s {
	case REBAL_ACTIVE:
		return "RebalActive"
	case REBAL_PENDING:
		return "RebalPending"
	case REBAL_MERGED:
		return "Merged"
	case REBAL_PENDING_DELETE:
		return "PendingDelete"
	default:
		return "Invalid"
	}
}

func (idx IndexInstMap) String() string {

	str := "\n"
	for i, index := range idx {
		str += fmt.Sprintf("\tInstanceId: %v ", i)
		str += fmt.Sprintf("Name: %v ", index.Defn.Name)
		str += fmt.Sprintf("Bucket: %v ", index.Defn.Bucket)
		str += fmt.Sprintf("State: %v ", index.State)
		str += fmt.Sprintf("Stream: %v ", index.Stream)
		str += fmt.Sprintf("RState: %v ", index.RState)
		str += fmt.Sprintf("Version: %v ", index.Version)
		str += fmt.Sprintf("ReplicaId: %v ", index.ReplicaId)
		str += "\n"
	}
	return str

}

func CopyIndexInstMap(inMap IndexInstMap) IndexInstMap {

	outMap := make(IndexInstMap)
	for k, v := range inMap {
		vv := v
		vv.Pc = v.Pc.Clone()
		outMap[k] = vv
	}
	return outMap
}

func MarshallIndexDefn(defn *IndexDefn) ([]byte, error) {

	buf, err := json.Marshal(&defn)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func UnmarshallIndexDefn(data []byte) (*IndexDefn, error) {

	defn := new(IndexDefn)
	if err := json.Unmarshal(data, defn); err != nil {
		return nil, err
	}

	return defn, nil
}

func MarshallIndexInst(inst *IndexInst) ([]byte, error) {

	buf, err := json.Marshal(&inst)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func UnmarshallIndexInst(data []byte) (*IndexInst, error) {

	inst := new(IndexInst)
	if err := json.Unmarshal(data, inst); err != nil {
		return nil, err
	}

	return inst, nil
}

func NewIndexDefnId() (IndexDefnId, error) {
	uuid, err := NewUUID()
	if err != nil {
		return IndexDefnId(0), err
	}

	return IndexDefnId(uuid.Uint64()), nil
}

func NewIndexInstId() (IndexInstId, error) {
	uuid, err := NewUUID()
	if err != nil {
		return IndexInstId(0), err
	}

	return IndexInstId(uuid.Uint64()), nil
}

func IsPartitioned(scheme PartitionScheme) bool {
	return len(scheme) != 0 && scheme != SINGLE
}

//IndexSnapType represents the snapshot type
//created in indexer storage
type IndexSnapType uint16

const (
	NO_SNAP IndexSnapType = iota
	DISK_SNAP
	INMEM_SNAP
	FORCE_COMMIT
)

func (s IndexSnapType) String() string {

	switch s {
	case NO_SNAP:
		return "NO_SNAP"
	case DISK_SNAP:
		return "DISK_SNAP"
	case INMEM_SNAP:
		return "INMEM_SNAP"
	case FORCE_COMMIT:
		return "FORCE_COMMIT"
	default:
		return "INVALID_SNAP_TYPE"
	}

}

//NOTE: This type needs to be in sync with smStrMap
type IndexType string

const (
	ForestDB        = "forestdb"
	MemDB           = "memdb"
	MemoryOptimized = "memory_optimized"
	PlasmaDB        = "plasma"
)

func IsValidIndexType(t string) bool {
	switch strings.ToLower(t) {
	case ForestDB, MemDB, MemoryOptimized, PlasmaDB:
		return true
	}

	return false
}

func IsEquivalentIndex(d1, d2 *IndexDefn) bool {

	if d1.Bucket != d2.Bucket ||
		d1.IsPrimary != d2.IsPrimary ||
		d1.ExprType != d2.ExprType ||
		d1.PartitionScheme != d2.PartitionScheme ||
		d1.HashScheme != d2.HashScheme ||
		d1.WhereExpr != d2.WhereExpr ||
		d1.RetainDeletedXATTR != d2.RetainDeletedXATTR {

		return false
	}

	if len(d1.SecExprs) != len(d2.SecExprs) {
		return false
	}

	for i, s1 := range d1.SecExprs {
		if s1 != d2.SecExprs[i] {
			return false
		}
	}

	if len(d1.PartitionKeys) != len(d2.PartitionKeys) {
		return false
	}

	for i, s1 := range d1.PartitionKeys {
		if s1 != d2.PartitionKeys[i] {
			return false
		}
	}

	if len(d1.Desc) != len(d2.Desc) {
		return false
	}

	for i, b1 := range d1.Desc {
		if b1 != d2.Desc[i] {
			return false
		}
	}

	return true
}

//
// IndexerError - Runtime Error between indexer and other modules
//
type IndexerErrCode int

const (
	TransientError IndexerErrCode = iota
	IndexNotExist
	InvalidBucket
	IndexerInRecovery
	IndexBuildInProgress
	IndexerNotActive
	RebalanceInProgress
	IndexAlreadyExist
	DropIndexInProgress
	IndexInvalidState
	BucketEphemeral
)

type IndexerError struct {
	Reason string
	Code   IndexerErrCode
}

func (e *IndexerError) Error() string {
	return e.Reason
}

func (e *IndexerError) ErrCode() IndexerErrCode {
	return e.Code
}

//MetadataRequestContext - communication context between manager and indexer
//Currently used by manager.MetadataNotifier interface

type DDLRequestSource byte

const (
	DDLRequestSourceUser DDLRequestSource = iota
	DDLRequestSourceRebalance
)

type MetadataRequestContext struct {
	ReqSource DDLRequestSource
}

func NewRebalanceRequestContext() *MetadataRequestContext {
	return &MetadataRequestContext{ReqSource: DDLRequestSourceRebalance}
}

func NewUserRequestContext() *MetadataRequestContext {
	return &MetadataRequestContext{ReqSource: DDLRequestSourceUser}
}

// Format of the data encoding, when it is being transferred over the wire
// from indexer to GsiClient during scan
type DataEncodingFormat uint32

const (
	DATA_ENC_JSON DataEncodingFormat = iota
	DATA_ENC_COLLATEJSON
)

var ErrUnexpectedDataEncFmt = errors.New("Unexpected data encoding format")
