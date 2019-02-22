// Copyright (c) 2014 Couchbase, Inc.
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
// except in compliance with the License. You may obtain a copy of the License at
//   http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed under the
// License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
// either express or implied. See the License for the specific language governing permissions
// and limitations under the License.

package client

import (
	"encoding/json"
	"fmt"
	"github.com/couchbase/gometa/common"
	c "github.com/couchbase/indexing/secondary/common"
	logging "github.com/couchbase/indexing/secondary/logging"
	mc "github.com/couchbase/indexing/secondary/manager/common"
)

/////////////////////////////////////////////////////////////////////////
// OpCode
////////////////////////////////////////////////////////////////////////

const (
	OPCODE_CREATE_INDEX               common.OpCode = common.OPCODE_CUSTOM + 1
	OPCODE_DROP_INDEX                               = OPCODE_CREATE_INDEX + 1
	OPCODE_BUILD_INDEX                              = OPCODE_DROP_INDEX + 1
	OPCODE_UPDATE_INDEX_INST                        = OPCODE_BUILD_INDEX + 1
	OPCODE_SERVICE_MAP                              = OPCODE_UPDATE_INDEX_INST + 1
	OPCODE_DELETE_BUCKET                            = OPCODE_SERVICE_MAP + 1
	OPCODE_INDEXER_READY                            = OPCODE_DELETE_BUCKET + 1
	OPCODE_CLEANUP_INDEX                            = OPCODE_INDEXER_READY + 1
	OPCODE_CLEANUP_DEFER_INDEX                      = OPCODE_CLEANUP_INDEX + 1
	OPCODE_CREATE_INDEX_REBAL                       = OPCODE_CLEANUP_DEFER_INDEX + 1
	OPCODE_BUILD_INDEX_REBAL                        = OPCODE_CREATE_INDEX_REBAL + 1
	OPCODE_DROP_INDEX_REBAL                         = OPCODE_BUILD_INDEX_REBAL + 1
	OPCODE_BROADCAST_STATS                          = OPCODE_DROP_INDEX_REBAL + 1
	OPCODE_BUILD_INDEX_RETRY                        = OPCODE_BROADCAST_STATS + 1
	OPCODE_RESET_INDEX                              = OPCODE_BUILD_INDEX_RETRY + 1
	OPCODE_CONFIG_UPDATE                            = OPCODE_RESET_INDEX + 1
	OPCODE_DROP_OR_PRUNE_INSTANCE                   = OPCODE_CONFIG_UPDATE + 1
	OPCODE_MERGE_PARTITION                          = OPCODE_DROP_OR_PRUNE_INSTANCE + 1
	OPCODE_PREPARE_CREATE_INDEX                     = OPCODE_MERGE_PARTITION + 1
	OPCODE_COMMIT_CREATE_INDEX                      = OPCODE_PREPARE_CREATE_INDEX + 1
	OPCODE_REBALANCE_RUNNING                        = OPCODE_COMMIT_CREATE_INDEX + 1
	OPCODE_CREATE_INDEX_DEFER_BUILD                 = OPCODE_REBALANCE_RUNNING + 1
	OPCODE_DROP_OR_PRUNE_INSTANCE_DDL               = OPCODE_CREATE_INDEX_DEFER_BUILD + 1
	OPCODE_CLEANUP_PARTITION                        = OPCODE_DROP_OR_PRUNE_INSTANCE_DDL + 1
	OPCODE_DROP_INSTANCE                            = OPCODE_CLEANUP_PARTITION + 1
	OPCODE_UPDATE_REPLICA_COUNT                     = OPCODE_DROP_INSTANCE + 1
	OPCODE_GET_REPLICA_COUNT                        = OPCODE_UPDATE_REPLICA_COUNT + 1
	OPCODE_CHECK_TOKEN_EXIST                        = OPCODE_GET_REPLICA_COUNT + 1
)

func Op2String(op common.OpCode) string {
	switch op {
	case OPCODE_CREATE_INDEX:
		return "OPCODE_CREATE_INDEX"
	case OPCODE_DROP_INDEX:
		return "OPCODE_DROP_INDEX"
	case OPCODE_BUILD_INDEX:
		return "OPCODE_BUILD_INDEX"
	case OPCODE_UPDATE_INDEX_INST:
		return "OPCODE_UPDATE_INDEX_INST"
	case OPCODE_SERVICE_MAP:
		return "OPCODE_SERVICE_MAP"
	case OPCODE_DELETE_BUCKET:
		return "OPCODE_DELETE_BUCKET"
	case OPCODE_INDEXER_READY:
		return "OPCODE_INDEXER_READY"
	case OPCODE_CLEANUP_INDEX:
		return "OPCODE_CLEANUP_INDEX"
	case OPCODE_CLEANUP_DEFER_INDEX:
		return "OPCODE_CLEANUP_DEFER_INDEX"
	case OPCODE_CREATE_INDEX_REBAL:
		return "OPCODE_CREATE_INDEX_REBAL"
	case OPCODE_BUILD_INDEX_REBAL:
		return "OPCODE_BUILD_INDEX_REBAL"
	case OPCODE_DROP_INDEX_REBAL:
		return "OPCODE_DROP_INDEX_REBAL"
	case OPCODE_BROADCAST_STATS:
		return "OPCODE_BROADCAST_STATS"
	case OPCODE_BUILD_INDEX_RETRY:
		return "OPCODE_BUILD_INDEX_RETRY"
	case OPCODE_RESET_INDEX:
		return "OPCODE_RESET_INDEX"
	case OPCODE_CONFIG_UPDATE:
		return "OPCODE_CONFIG_UPDATE"
	case OPCODE_DROP_OR_PRUNE_INSTANCE:
		return "OPCODE_DROP_OR_PRUNE_INSTANCE"
	case OPCODE_MERGE_PARTITION:
		return "OPCODE_MERGE_PARTITION"
	case OPCODE_PREPARE_CREATE_INDEX:
		return "OPCODE_PREPARE_CREATE_INDEX"
	case OPCODE_COMMIT_CREATE_INDEX:
		return "OPCODE_COMMIT_CREATE_INDEX"
	case OPCODE_REBALANCE_RUNNING:
		return "OPCODE_REBALANCE_RUNNING"
	case OPCODE_CREATE_INDEX_DEFER_BUILD:
		return "OPCODE_CREATE_INDEX_DEFER_BUILD"
	case OPCODE_DROP_OR_PRUNE_INSTANCE_DDL:
		return "OPCODE_DROP_OR_PRUNE_INSTANCE_DDL"
	case OPCODE_CLEANUP_PARTITION:
		return "OPCODE_CLEANUP_PARTITION"
	case OPCODE_DROP_INSTANCE:
		return "OPCODE_DROP_INSTANCE"
	case OPCODE_UPDATE_REPLICA_COUNT:
		return "OPCODE_UPDATE_REPLICA_COUNT"
	case OPCODE_GET_REPLICA_COUNT:
		return "OPCODE_GET_REPLICA_COUNT"
	case OPCODE_CHECK_TOKEN_EXIST:
		return "OPCODE_CHECK_TOKEN_EXIST"
	}
	return fmt.Sprintf("%v", op)
}

/////////////////////////////////////////////////////////////////////////
// Index List
////////////////////////////////////////////////////////////////////////

type IndexIdList struct {
	DefnIds []uint64 `json:"defnIds,omitempty"`
}

/////////////////////////////////////////////////////////////////////////
// Service Map
////////////////////////////////////////////////////////////////////////

type ServiceMap struct {
	IndexerId      string `json:"indexerId,omitempty"`
	ScanAddr       string `json:"scanAddr,omitempty"`
	HttpAddr       string `json:"httpAddr,omitempty"`
	AdminAddr      string `json:"adminAddr,omitempty"`
	NodeAddr       string `json:"nodeAddr,omitempty"`
	ServerGroup    string `json:"serverGroup,omitempty"`
	NodeUUID       string `json:"nodeUUID,omitempty"`
	IndexerVersion uint64 `json:"indexerVersion,omitempty"`
	ClusterVersion uint64 `json:"clusterVersion,omitempty"`
	ExcludeNode    string `json:"excludeNode,omitempty"`
	StorageMode    uint64 `json:"storageMode,omitempty"`
}

/////////////////////////////////////////////////////////////////////////
// Index Stats
////////////////////////////////////////////////////////////////////////

type IndexStats struct {
	Stats c.Statistics `json:"stats,omitempty"`
}

/////////////////////////////////////////////////////////////////////////
// Create/Alter Index
////////////////////////////////////////////////////////////////////////

type PrepareCreateRequestOp int

const (
	PREPARE PrepareCreateRequestOp = iota
	CANCEL_PREPARE
)

type PrepareCreateRequest struct {
	Op PrepareCreateRequestOp `json:"op,omitempty"`

	DefnId      c.IndexDefnId `json:"defnId,omitempty"`
	RequesterId string        `json:"requestId,omitempty"`
	Timeout     int64         `json:"timeout,omitempty"`
	StartTime   int64         `json:"startTime,omitempty"`
}

type PrepareCreateResponse struct {
	// Prepare
	Accept bool `json:"accept,omitempty"`
}

type CommitCreateRequestOp int

const (
	NEW_INDEX CommitCreateRequestOp = iota
	ADD_REPLICA
	DROP_REPLICA
)

type CommitCreateRequest struct {
	Op          CommitCreateRequestOp         `json:"op,omitempty"`
	DefnId      c.IndexDefnId                 `json:"defnId,omitempty"`
	RequesterId string                        `json:"requesterId,omitempty"`
	Definitions map[c.IndexerId][]c.IndexDefn `json:"definitions,omitempty"`
	RequestId   uint64                        `json:"requestId,omitempty"`
}

type CommitCreateResponse struct {
	Accept bool `json:"accept,omitempty"`
}

/////////////////////////////////////////////////////////////////////////
// Check Token
////////////////////////////////////////////////////////////////////////

const (
	CREATE_INDEX_TOKEN  uint32 = 0x0001
	DROP_INDEX_TOKEN           = 0x0010
	DROP_INSTANCE_TOKEN        = 0x0100
)

type CheckToken struct {
	DefnId c.IndexDefnId `json:"defnId,omitempty"`
	InstId c.IndexInstId `json:"instId,omitempty"`
	Flag   uint32        `json:"flag,omitempty"`
}

/////////////////////////////////////////////////////////////////////////
// marshalling/unmarshalling
////////////////////////////////////////////////////////////////////////

func unmarshallIndexTopology(data []byte) (*mc.IndexTopology, error) {

	topology := new(mc.IndexTopology)
	if err := json.Unmarshal(data, topology); err != nil {
		return nil, err
	}

	return topology, nil
}

func marshallIndexTopology(topology *mc.IndexTopology) ([]byte, error) {

	buf, err := json.Marshal(&topology)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func BuildIndexIdList(ids []c.IndexDefnId) *IndexIdList {
	list := new(IndexIdList)
	list.DefnIds = make([]uint64, len(ids))
	for i, id := range ids {
		list.DefnIds[i] = uint64(id)
	}
	return list
}

func UnmarshallIndexIdList(data []byte) (*IndexIdList, error) {

	list := new(IndexIdList)
	if err := json.Unmarshal(data, list); err != nil {
		return nil, err
	}

	return list, nil
}

func MarshallIndexIdList(list *IndexIdList) ([]byte, error) {

	buf, err := json.Marshal(&list)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func UnmarshallServiceMap(data []byte) (*ServiceMap, error) {

	logging.Debugf("UnmarshallServiceMap: %v", string(data))

	list := new(ServiceMap)
	if err := json.Unmarshal(data, list); err != nil {
		return nil, err
	}

	return list, nil
}

func MarshallServiceMap(srvMap *ServiceMap) ([]byte, error) {

	buf, err := json.Marshal(&srvMap)
	if err != nil {
		return nil, err
	}

	logging.Debugf("MarshallServiceMap: %v", string(buf))

	return buf, nil
}

func UnmarshallIndexStats(data []byte) (*IndexStats, error) {

	logging.Debugf("UnmarshallIndexStats: %v", string(data))

	stats := new(IndexStats)
	if err := json.Unmarshal(data, stats); err != nil {
		return nil, err
	}

	return stats, nil
}

func MarshallIndexStats(stats *IndexStats) ([]byte, error) {

	buf, err := json.Marshal(&stats)
	if err != nil {
		return nil, err
	}

	logging.Debugf("MarshallIndexStats: %v", string(buf))

	return buf, nil
}

func UnmarshallPrepareCreateRequest(data []byte) (*PrepareCreateRequest, error) {

	logging.Debugf("UnmarshallPrepareCreateRequest: %v", string(data))

	prepareCreateRequest := new(PrepareCreateRequest)
	if err := json.Unmarshal(data, prepareCreateRequest); err != nil {
		return nil, err
	}

	return prepareCreateRequest, nil
}

func MarshallPrepareCreateRequest(prepareCreateRequest *PrepareCreateRequest) ([]byte, error) {

	buf, err := json.Marshal(&prepareCreateRequest)
	if err != nil {
		return nil, err
	}

	logging.Debugf("MarshallPrepareCreateRequest: %v", string(buf))

	return buf, nil
}

func UnmarshallPrepareCreateResponse(data []byte) (*PrepareCreateResponse, error) {

	logging.Debugf("UnmarshallPrepareCreateResponse: %v", string(data))

	prepareCreateResponse := new(PrepareCreateResponse)
	if err := json.Unmarshal(data, prepareCreateResponse); err != nil {
		return nil, err
	}

	return prepareCreateResponse, nil
}

func MarshallPrepareCreateResponse(prepareCreateResponse *PrepareCreateResponse) ([]byte, error) {

	buf, err := json.Marshal(&prepareCreateResponse)
	if err != nil {
		return nil, err
	}

	logging.Debugf("MarshallPrepareCreateResponse: %v", string(buf))

	return buf, nil
}

func UnmarshallCommitCreateRequest(data []byte) (*CommitCreateRequest, error) {

	logging.Debugf("UnmarshallCommitCreateRequest: %v", string(data))

	commitCreateRequest := new(CommitCreateRequest)
	if err := json.Unmarshal(data, commitCreateRequest); err != nil {
		return nil, err
	}

	return commitCreateRequest, nil
}

func MarshallCommitCreateRequest(commitCreateRequest *CommitCreateRequest) ([]byte, error) {

	buf, err := json.Marshal(&commitCreateRequest)
	if err != nil {
		return nil, err
	}

	logging.Debugf("MarshallCommitCreateRequest: %v", string(buf))

	return buf, nil
}

func UnmarshallCommitCreateResponse(data []byte) (*CommitCreateResponse, error) {

	logging.Debugf("UnmarshallCommitCreateResponse: %v", string(data))

	commitCreateResponse := new(CommitCreateResponse)
	if err := json.Unmarshal(data, commitCreateResponse); err != nil {
		return nil, err
	}

	return commitCreateResponse, nil
}

func MarshallCommitCreateResponse(commitCreateResponse *CommitCreateResponse) ([]byte, error) {

	buf, err := json.Marshal(&commitCreateResponse)
	if err != nil {
		return nil, err
	}

	logging.Debugf("MarshallCommitCreateResponse: %v", string(buf))

	return buf, nil
}

func UnmarshallChecKToken(data []byte) (*CheckToken, error) {

	checkToken := new(CheckToken)
	if err := json.Unmarshal(data, checkToken); err != nil {
		return nil, err
	}

	return checkToken, nil
}

func MarshallCheckToken(checkToken *CheckToken) ([]byte, error) {

	buf, err := json.Marshal(&checkToken)
	if err != nil {
		return nil, err
	}

	return buf, nil
}
