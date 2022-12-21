// @copyright 2022-Present Couchbase, Inc.
//
// Use of this software is governed by the Business Source License included
// in the file licenses/BSL-Couchbase.txt.  As of the Change Date specified
// in that file, in accordance with the Business Source License, use of this
// software will be governed by the Apache License, Version 2.0, included in
// the file licenses/APL2.txt.

package indexer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/couchbase/cbauth/metakv"
	"io/ioutil"
	l "log"
	"strings"
	"sync"
	"time"

	"github.com/couchbase/indexing/secondary/common"
	"github.com/couchbase/indexing/secondary/logging"
	"github.com/couchbase/indexing/secondary/manager"
)

////////////////////////////////////////////////////////////////////////////////////////////////////
// Pauser class - Perform the Pause of a given bucket (similar to Rebalancer's role).
// This is used only on the master node of a task_PAUSE task to do the GSI orchestration.
////////////////////////////////////////////////////////////////////////////////////////////////////

// Pauser object holds the state of Pause orchestration
type Pauser struct {
	// nodeDir is "node_<nodeId>/" for this node, where nodeId is the 32-digit hex ID from ns_server
	nodeDir string

	// otherIndexAddrs is "host:port" to all the known Index Service nodes EXCLUDING this one
	otherIndexAddrs []string

	// pauseMgr is the singleton parent of this object
	pauseMgr *PauseServiceManager

	// task is the task_PAUSE task we are executing (protected by task.taskMu). It lives in the
	// pauseMgr.tasks map (protected by pauseMgr.tasksMu). Only the current object should change or
	// delete task at this point, but GetTaskList and other processing may concurrently read it.
	// Thus Pauser needs to write lock task.taskMu for changes but does not need to read lock it.
	task *taskObj

	pauseToken *PauseToken

	waitForTokenPublish chan struct{}

	metakvCancel chan struct{}
	metakvMutex  sync.RWMutex

	wg          sync.WaitGroup

	nodeUUID   string

	// lock protecting access to maps like transferTokens, sourceTokens etc.
	mu sync.RWMutex
	masterTokens, followerTokens map[string]*PauseStateToken

	cleanupOnce sync.Once
}

// NewPauser creates a Pauser instance to execute the given task. It saves a pointer to itself in
// task.pauser (visible to pauseMgr parent) and launches a goroutine for the work.
//
//	pauseMgr - parent object (singleton)
//	task - the task_PAUSE task this object will execute
//	master - true iff this node is the master
func NewPauser(pauseMgr *PauseServiceManager, task *taskObj, master bool, pauseToken *PauseToken) *Pauser {
	logging.Infof("amd: NewPauser: master[%v] pt[%v]", master, pauseToken)

	pauser := &Pauser{
		pauseMgr: pauseMgr,
		task:     task,
		nodeDir:  "node_" + string(pauseMgr.genericMgr.nodeInfo.NodeID) + "/",
		pauseToken: pauseToken,
		waitForTokenPublish: make(chan struct{}),
		metakvCancel: make(chan struct{}),
		masterTokens: make(map[string]*PauseStateToken),
		followerTokens: make(map[string]*PauseStateToken),
	}

	task.taskMu.Lock()
	task.pauser = pauser
	task.taskMu.Unlock()

	// TODO: start observer for PauseStateTokens
	go pauser.observePause()

	// TODO: if master generate PauseStateTokens and send on meta KV.
	if master {
		go pauser.initPauseAsync()
	} else {
		// if not master, no need to wait for publishing of tokens
		close(pauser.waitForTokenPublish)
	}


	return pauser
}

////////////////////////////////////////////////////////////////////////////////////////////////////
// Methods
////////////////////////////////////////////////////////////////////////////////////////////////////

func (p *Pauser) getIndexerUuids() (indexerUuids []string, err error) {

	p.pauseMgr.genericMgr.cinfo.Lock()
	defer p.pauseMgr.genericMgr.cinfo.Unlock()

	if err := p.pauseMgr.genericMgr.cinfo.FetchNodesAndSvsInfo(); err != nil {
		logging.Errorf("Pauser::getIndexerUuids Error Fetching Cluster Information %v", err)
		return nil, err
	}

	nids := p.pauseMgr.genericMgr.cinfo.GetNodeIdsByServiceType(common.INDEX_HTTP_SERVICE)
	url := "/nodeuuid"

	for _, nid := range nids {
		haddr, err := p.pauseMgr.genericMgr.cinfo.GetServiceAddress(nid, common.INDEX_HTTP_SERVICE, true)
		if err != nil {
			return nil, err
		}

		resp, err := getWithAuth(haddr + url)
		if err != nil {
			logging.Errorf("Pauser::getIndexerUuids Unable to Fetch Node UUID %v %v", haddr, err)
			return nil, err
		} else {
			bytes, _ := ioutil.ReadAll(resp.Body)
			defer resp.Body.Close()

			uuid := string(bytes)
			indexerUuids = append(indexerUuids, uuid)
		}
	}

	return indexerUuids, nil
}

func (p *Pauser) initPauseAsync() {

	// TODO: init progress update

	// generate pause state tokens
	psts, err := p.generatePauseStateTokens()
	if err != nil {
		logging.Errorf("err[%v]", err)
	}

	// publish pause state tokens
	// will crash if cannot set in metaKV even after retries.
	p.publishPauseStateTokens(psts)

	// Ask observe to continue
	close(p.waitForTokenPublish)

}

func setPauseStateTokenInMetakv(pstId string, pst *PauseStateToken) {

	fn := func(r int, err error) error {
		if r > 0 {
			logging.Warnf("Pauser::setPauseStateTokenInMetakv error=%v Retrying (%d)", err, r)
		}
		err = common.MetakvSet(PauseMetakvDir+pstId, pst)
		return err
	}

	rh := common.NewRetryHelper(10, time.Second, 1, fn)
	err := rh.Run()

	if err != nil {
		logging.Fatalf("Pauser::setPauseStateTokenInMetakv Unable to set PauseStateToken In "+
			"Meta Storage. %v %v. Err %v", pstId, pst, err)
		common.CrashOnError(err)
	}
}

func (p *Pauser) publishPauseStateTokens(psts map[string]*PauseStateToken) {
	for pstId, pst := range psts {
		setPauseStateTokenInMetakv(pstId, pst)
		logging.Infof("Pauser::publishPauseStateTokens Published pause state token: %v", pstId)
	}
}

func (p *Pauser) generatePauseStateTokens() (map[string]*PauseStateToken, error) {
	indexerUuids, err := p.getIndexerUuids()
	if err != nil || len(indexerUuids) < 1 {
		// TODO: handle error
		logging.Errorf("Pauser::initPauseAsync: Error getting indexer node UUIDs err[%v] indexerUuids[%v]", err, indexerUuids)
		return nil, err
	}

	psts := make(map[string]*PauseStateToken)

	for _, uuid := range indexerUuids {
		pst := &PauseStateToken{
			MasterId: p.nodeUUID,
			FollowerId: uuid,
			TaskId: p.task.taskId,
			State: PauseStateTokenPosted,
			BucketUuid: p.task.bucketUuid,
		}

		ustr, err := common.NewUUID()
		if err != nil {
			err = fmt.Errorf("Could not generate uuid! err[%v]", err)
			return nil, err

		}

		pstId := fmt.Sprintf("%s%s", PauseStateTokenTag, ustr.Str())

		if oldPst, ok := psts[pstId]; ok {
			err := fmt.Errorf("collision! pstId[%v] oldPst[%v] pst[%v]", pstId, oldPst, pst)
			return nil, err
		}
		psts[pstId] = pst
	}

	return psts, nil
}

func (p *Pauser) observePause() {
	logging.Infof("amd: Pauser::observePause %v master:%v", p.pauseToken, p.task.isMaster())

	<-p.waitForTokenPublish

	err := metakv.RunObserveChildren(PauseMetakvDir, p.processStateTokens, p.metakvCancel)
	if err != nil {
		logging.Infof("Pauser::observePause Exiting On Metakv Error %v", err)
		// TODO: Implement cleanup
		//p.finishPause(err)
	}

	logging.Infof("amd: Pauser::observePause exiting err %v", err)
}

// metakv callback, not intended to be called otherwise
func (p *Pauser) processStateTokens(kve metakv.KVEntry) error {

	if kve.Path == buildMetakvPathForPauseToken(p.pauseToken) {
		logging.Infof("amd: Pauser::processStateTokens PauseToken %v %s", kve.Path, kve.Value)
		if kve.Value == nil {
			logging.Infof("Pauser::processStateTokens PauseToken Deleted. Mark Done.")
			p.cancelMetakv()

			// TODO: Implement cleanup
			// p.finishPause(nil)
		}
	} else if strings.Contains(kve.Path, PauseStateTokenPathPrefix) {
		if kve.Value != nil {
			pstId, pst, err := decodePauseStateToken(kve.Path, kve.Value)
			if err != nil {
				logging.Errorf("amd: Pauser::processStateTokens Unable to decode transfer token. Ignored.")
				return nil
			}

			p.processPauseStateToken(pstId, pst)

		} else {
			logging.Infof("amd: Pauser::processStateTokens Received empty or deleted transfer token %v", kve.Path)
		}
	}

	return nil
}

func (p *Pauser) processPauseStateToken(pstId string, pst *PauseStateToken) {
	logging.Infof("amd: Pauser::processPauseStateToken pstId[%v] pst[%v]", pstId, pst)
	if !p.addToWaitGroup() {
		return
	}

	// Can't stop Pauser till this returns
	defer p.wg.Done()

	// TODO: Check DDL running?

	// "processed" var ensures only the incoming token state gets processed by this
	// call, as metakv will call parent processTokens again for each TT state change.
	var processed bool

	nodeUUID := string(p.pauseMgr.nodeInfo.NodeID)

	if pst.MasterId == nodeUUID {
		processed = p.processPauseStateTokenAsMaster(pstId, pst)
	}

	if (pst.FollowerId == nodeUUID && !processed) {
		p.processPauseStateTokenAsFollower(pstId, pst)
	}
}

// Often, metaKV can send multiple notifications for the same state change
// (probably due to the eventual consistent nature of metaKV).
// will keep a track of all state changes in its in-memory book-keeping and
// ignores the duplicate notifications
func (p *Pauser) checkValidNotifyState(pstId string, pst *PauseStateToken, caller string) bool {

	//// As the default state is "PauseStateTokenPosted"
	//// do not check for valid state changes for this state
	//if pst.State == PauseStateTokenPosted {
	//	return true
	//}

	p.mu.RLock()
	defer p.mu.RUnlock()

	var inMemToken *PauseStateToken
	var ok bool

	if caller == "master" {
		inMemToken, ok = p.masterTokens[pstId]
	} else if caller == "follower" {
		inMemToken, ok = p.followerTokens[pstId]
	}

	if ok {
		// < for invalid state change
		// == for duplicate notification
		if pst.State <= inMemToken.State {
			logging.Warnf("Pauser::checkValidNotifyState Detected Invalid State "+
				"Change Notification for %v. Token Id %v Local State %v Metakv State %v",
				caller, pstId, inMemToken.State, pst.State)
			return false
		}
	}
	return true
}

func (p *Pauser) updateInMemToken(pstId string, pst *PauseStateToken, caller string) {

	p.mu.Lock()
	defer p.mu.Unlock()

	if caller == "master" {
		p.masterTokens[pstId] = pst.Clone()
	} else if caller == "follower" {
		p.followerTokens[pstId] = pst.Clone()
	}
}

func (p *Pauser) checkAllTokensDone() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	for pstId, pst := range p.masterTokens {
		if pst.State != PauseStateTokenProcessed {
			logging.Infof("Pauser::checkAllTokensDone PauseStateToken: %v is in state: %v", pstId, pst.State)
			return false
		}
	}
	return true
}

func (p *Pauser) finishPause(err error) {
	//sr.retErr = err
	p.cleanupOnce.Do(p.doFinish)
}

func (p *Pauser) doFinish() {
	logging.Infof("amd: Pauser::doFinish Cleanup: %v", nil)//sr.retErr)

	// TODO: signal others that we are cleaning up
	//close(sr.done)

	p.cancelMetakv()
	p.wg.Wait()
	//p.cb.done(sr.retErr, sr.cancel)

	// TODO: call done callback
}


func (p *Pauser) processPauseStateTokenAsMaster(pstId string, pst *PauseStateToken) bool {

	logging.Infof("amd: processPauseStateTokenAsMaster: pstId[%v] pst[%v] state[%v]", pstId, pst, pst.State)

	if pst.TaskId != p.task.taskId {
		logging.Warnf("Pauser::processPauseStateTokenAsMaster Found PauseStateToken with Unknown "+
			"TaskId. Local taskId %v Token %v. Ignored.", p.task.taskId, pst)
		return true
	}

	if pst.Error != "" {
		logging.Errorf("Pauser::processPauseStateTokenAsMaster Detected PauseStateToken in Error state %v. Abort.", pst)

		p.cancelMetakv()

		// TODO: cleanup
		//go p.finishPause(errors.New(pst.Error))

		return true
	}

	if !p.checkValidNotifyState(pstId, pst, "master") {
		return true
	}

	switch pst.State {

	case PauseStateTokenPosted:
		// ignore?
		return false

	case PauseStateTokenInProgess:
		// follower owns now, just mark in memory maps.
		p.updateInMemToken(pstId, pst, "master")
		return false

	case PauseStateTokenProcessed:
		err := common.MetakvDel(PauseMetakvDir + pstId)
		if err != nil {
			l.Fatalf("Pauser::processPauseStateTokenAsMaster Unable to set PauseStateToken In "+
				"Meta Storage. %v. Err %v", pst, err)
			common.CrashOnError(err)
		}

		p.updateInMemToken(pstId, pst, "master")

		if p.checkAllTokensDone() {
			// All the followers are done upload work

			// TODO: set progress 100%
			//if sr.cb.progress != nil {
			//	sr.cb.progress(1.0, sr.cancel)
			//}

			logging.Infof("amd: Pauser::processPauseStateTokenAsMaster No Tokens Found. Mark Done.")

			p.cancelMetakv()
			// TODO: cleanup
			go p.finishPause(nil)
		}

		return true

	default:
		return false
	}

}


func (p *Pauser) processPauseStateTokenAsFollower(pstId string, pst *PauseStateToken) bool {

	logging.Infof("amd: processPauseStateTokenAsFollower: pstId[%v] pst[%v] state[%s]", pstId, pst, pst.State)

	if pst.TaskId != p.task.taskId {
		logging.Warnf("Pauser::processPauseStateTokenAsFollower Found PauseStateToken with Unknown "+
			"TaskId. Local taskId %v Token %v. Ignored.", p.task.taskId, pst)
		return true
	}

	if !p.checkValidNotifyState(pstId, pst, "follower") {
		return true
	}

	switch pst.State {

	case PauseStateTokenPosted:

		p.updateInMemToken(pstId, pst, "follower")

		// move to in-progress
		pst.State = PauseStateTokenInProgess
		setPauseStateTokenInMetakv(pstId, pst)

		return true

	case PauseStateTokenInProgess:
		p.updateInMemToken(pstId, pst, "follower")

		// do actual pause work
		go p.startPauseUpload(pstId, pst)

		return true

	case PauseStateTokenProcessed:
		// Update in-mem book keeping and do not process the token
		p.updateInMemToken(pstId, pst, "follower")
		return false

	default:
		return false
	}
}

func (p *Pauser) startPauseUpload(pstId string, pst *PauseStateToken) {
	logging.Infof("amd: startPauseUpload: pstId[%v] pst[%v]", pstId, pst)
	defer logging.Infof("amd: startPauseUpload: Donne pstId[%v] pst[%v]", pstId, pst)

	if !p.addToWaitGroup() {
		return
	}
	defer p.wg.Done()

	// TODO: move work from run to this

	// upload all the things
	time.Sleep(5 * time.Second)

	// done uploading, change state and set
	pst.State = PauseStateTokenProcessed
	setPauseStateTokenInMetakv(pstId, pst)
}

type PauseState byte

const (
	PauseStateTokenPosted PauseState = iota
	PauseStateTokenInProgess
	PauseStateTokenProcessed
	PauseStateTokenError
)

func (s PauseState) String() string {
	switch s {
	case PauseStateTokenPosted:
		return "PauseStateTokenPosted"
	case PauseStateTokenInProgess:
		return "PauseStateTokenInProgess"
	case PauseStateTokenProcessed:
		return "PauseStateTokenProcessed"
	case PauseStateTokenError:
		return "PauseStateTokenError"
	}

	return "PST-UNKNOWN"
}

type PauseStateToken struct {
	MasterId     string
	FollowerId   string
	TaskId       string
	State        PauseState
	BucketUuid   string
	Error        string
}

func (pst *PauseStateToken) Clone() *PauseStateToken {
	pst1 := *pst
	pst2 := pst1
	return &pst2
}

func decodePauseStateToken(path string, value []byte) (string, *PauseStateToken, error) {

	pstIdPos := strings.Index(path, PauseStateTokenTag)
	pstId := path[pstIdPos:]

	pst := &PauseStateToken{}
	err := json.Unmarshal(value, pst)
	if err != nil {
		l.Fatalf("decodePauseStateToken Failed unmarshalling value for %s: %s\n%s",
			path, err.Error(), string(value))
		return "", nil, err
	}

	return pstId, pst, nil

}

func (p *Pauser) addToWaitGroup() bool {
	p.metakvMutex.Lock()
	defer p.metakvMutex.Unlock()

	if p.metakvCancel != nil {
		p.wg.Add(1)
		return true
	}
	return false
}


func (p *Pauser) cancelMetakv() {
	p.metakvMutex.Lock()
	defer p.metakvMutex.Unlock()

	if p.metakvCancel != nil {
		close(p.metakvCancel)
		p.metakvCancel = nil
	}
}

// restGetLocalIndexMetadataBinary calls the /getLocalndexMetadata REST API (request_handler.go) via
// self-loopback to get the index metadata for the current node and the task's bucket (tenant). This
// verifies it can be unmarshaled, but it returns a checksummed and optionally compressed byte slice
// version of the data rather than the unmarshaled object.
func (this *Pauser) restGetLocalIndexMetadataBinary(compress bool) ([]byte, *manager.LocalIndexMetadata, error) {
	const _restGetLocalIndexMetadataBinary = "Pauser::restGetLocalIndexMetadataBinary:"

	url := fmt.Sprintf("%v/getLocalIndexMetadata?useETag=false&bucket=%v",
		this.pauseMgr.httpAddr, this.task.bucket)
	resp, err := getWithAuth(url)
	if err != nil {
		this.failPause(_restGetLocalIndexMetadataBinary, fmt.Sprintf("getWithAuth(%v)", url), err)
		return nil, nil, err
	}
	defer resp.Body.Close()

	byteSlice, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		this.failPause(_restGetLocalIndexMetadataBinary, "ReadAll(resp.Body)", err)
		return nil, nil, err
	}

	// Verify response can be unmarshaled
	metadata := new(manager.LocalIndexMetadata)
	err = json.Unmarshal(byteSlice, metadata)
	if err != nil {
		this.failPause(_restGetLocalIndexMetadataBinary, "Unmarshal localMeta", err)
		return nil, nil, err
	}
	if len(metadata.IndexDefinitions) == 0 {
		return nil, nil, nil
	}

	// Return checksummed and optionally compressed byte slice, not the unmarshaled object
	return common.ChecksumAndCompress(byteSlice, compress), metadata, nil
}

// failPause logs an error using the caller's logPrefix and a provided context string and aborts the
// Pause task. If there is a set of known Indexer nodes, it will also try to notify them.
func (this *Pauser) failPause(logPrefix string, context string, error error) {
	logging.Errorf("%v Aborting Pause task %v due to %v error: %v", logPrefix,
		this.task.taskId, context, error)

	// Mark the task as failed directly here on master node (avoids dependency on loopback REST)
	this.task.TaskObjSetFailed(error.Error())

	// Notify other Index nodes
	this.pauseMgr.RestNotifyFailedTask(this.otherIndexAddrs, this.task, error.Error())
}

// run is a goroutine for the main body of Pause work for this.task.
//
//	master - true iff this node is the master
func (this *Pauser) run(master bool) {
	const _run = "Pauser::run:"

	// Get the list of Index node host:port addresses EXCLUDING this one
	this.otherIndexAddrs = this.pauseMgr.GetIndexerNodeAddresses(this.pauseMgr.httpAddr)

	var byteSlice []byte
	var err error
	reader := bytes.NewReader(nil)

	/////////////////////////////////////////////
	// Work done by master only
	/////////////////////////////////////////////

	if master {
		// Write the version.json file to the archive
		byteSlice = []byte(fmt.Sprintf("{\"version\":%v}\n", ARCHIVE_VERSION))
		reader.Reset(byteSlice)
		err = Upload(this.task.archivePath, FILENAME_VERSION, reader)
		if err != nil {
			this.failPause(_run, "Upload "+FILENAME_VERSION, err)
			return
		}

		// Notify the followers to start working on this task
		this.pauseMgr.RestNotifyPause(this.otherIndexAddrs, this.task)
	} // if master

	/////////////////////////////////////////////
	// Work done by both master and followers
	/////////////////////////////////////////////

	// nodePath is the path to the node-specific archive subdirectory for the current node
	nodePath := this.task.archivePath + this.nodeDir

	// Get the index metadata from all nodes and write it as a single file to the archive
	byteSlice, indexMetadata, err := this.restGetLocalIndexMetadataBinary(true)
	if err != nil {
		this.failPause(_run, "getLocalInstanceMetadata", err)
		return
	}
	if byteSlice == nil {
		// there are no indexes on this node for bucket. pause is a no-op
		logging.Infof("Pauser::run pause is a no-op for bucket %v-%v", this.task.bucket, this.task.bucketUuid)
		return
	}
	reader.Reset(byteSlice)
	err = Upload(nodePath, FILENAME_METADATA, reader)
	if err != nil {
		this.failPause(_run, "Upload "+FILENAME_METADATA, err)
		return
	}

	getIndexInstanceIds := func(indexMetadata manager.LocalIndexMetadata) []common.IndexInstId {
		res := make([]common.IndexInstId, 0, len(indexMetadata.IndexDefinitions))
		for _, topology := range indexMetadata.IndexTopologies {
			for _, indexDefn := range topology.Definitions {
				res = append(res, common.IndexInstId(indexDefn.Instances[0].InstId))
			}
		}
		logging.Tracef("Pauser::getIndexInstanceId index instance ids: %v for bucket %v", res, this.task.bucket)
		return res
	}

	// Write the persistent stats to the archive
	byteSlice, err = this.pauseMgr.genericMgr.statsMgr.GetStatsForIndexesToBePersisted(getIndexInstanceIds(*indexMetadata), true)
	if err != nil {
		this.failPause(_run, "GetStatsForIndexesToBePersisted", err)
		return
	}
	reader.Reset(byteSlice)
	err = Upload(nodePath, FILENAME_STATS, reader)
	if err != nil {
		this.failPause(_run, "Upload "+FILENAME_STATS, err)
		return
	}

	// kjc implement Pause
}
