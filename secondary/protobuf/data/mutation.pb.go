// Code generated by protoc-gen-go. DO NOT EDIT.
// source: mutation.proto

package protobuf

import (
	fmt "fmt"
	proto "github.com/golang/protobuf/proto"
	math "math"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion2 // please upgrade the proto package

// List of possible mutation commands.
type Command int32

const (
	Command_Upsert         Command = 1
	Command_Deletion       Command = 2
	Command_UpsertDeletion Command = 3
	Command_Sync           Command = 4
	Command_DropData       Command = 5
	Command_StreamBegin    Command = 6
	Command_StreamEnd      Command = 7
)

var Command_name = map[int32]string{
	1: "Upsert",
	2: "Deletion",
	3: "UpsertDeletion",
	4: "Sync",
	5: "DropData",
	6: "StreamBegin",
	7: "StreamEnd",
}

var Command_value = map[string]int32{
	"Upsert":         1,
	"Deletion":       2,
	"UpsertDeletion": 3,
	"Sync":           4,
	"DropData":       5,
	"StreamBegin":    6,
	"StreamEnd":      7,
}

func (x Command) Enum() *Command {
	p := new(Command)
	*p = x
	return p
}

func (x Command) String() string {
	return proto.EnumName(Command_name, int32(x))
}

func (x *Command) UnmarshalJSON(data []byte) error {
	value, err := proto.UnmarshalJSONEnum(Command_value, data, "Command")
	if err != nil {
		return err
	}
	*x = Command(value)
	return nil
}

func (Command) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_abb5a9726058e37d, []int{0}
}

type ProjectorVersion int32

const (
	ProjectorVersion_V5_1_0 ProjectorVersion = 1
	ProjectorVersion_V5_1_1 ProjectorVersion = 2
	ProjectorVersion_V5_5_1 ProjectorVersion = 3
	ProjectorVersion_V5_6_5 ProjectorVersion = 4
)

var ProjectorVersion_name = map[int32]string{
	1: "V5_1_0",
	2: "V5_1_1",
	3: "V5_5_1",
	4: "V5_6_5",
}

var ProjectorVersion_value = map[string]int32{
	"V5_1_0": 1,
	"V5_1_1": 2,
	"V5_5_1": 3,
	"V5_6_5": 4,
}

func (x ProjectorVersion) Enum() *ProjectorVersion {
	p := new(ProjectorVersion)
	*p = x
	return p
}

func (x ProjectorVersion) String() string {
	return proto.EnumName(ProjectorVersion_name, int32(x))
}

func (x *ProjectorVersion) UnmarshalJSON(data []byte) error {
	value, err := proto.UnmarshalJSONEnum(ProjectorVersion_value, data, "ProjectorVersion")
	if err != nil {
		return err
	}
	*x = ProjectorVersion(value)
	return nil
}

func (ProjectorVersion) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_abb5a9726058e37d, []int{1}
}

// A single mutation message that will framed and transported by router.
// For efficiency mutations from mutiple vbuckets (bounded to same connection)
// can be packed into the same message.
type Payload struct {
	Version *uint32 `protobuf:"varint,1,req,name=version" json:"version,omitempty"`
	// -- Following fields are mutually exclusive --
	Vbkeys               []*VbKeyVersions `protobuf:"bytes,2,rep,name=vbkeys" json:"vbkeys,omitempty"`
	Vbmap                *VbConnectionMap `protobuf:"bytes,3,opt,name=vbmap" json:"vbmap,omitempty"`
	XXX_NoUnkeyedLiteral struct{}         `json:"-"`
	XXX_unrecognized     []byte           `json:"-"`
	XXX_sizecache        int32            `json:"-"`
}

func (m *Payload) Reset()         { *m = Payload{} }
func (m *Payload) String() string { return proto.CompactTextString(m) }
func (*Payload) ProtoMessage()    {}
func (*Payload) Descriptor() ([]byte, []int) {
	return fileDescriptor_abb5a9726058e37d, []int{0}
}

func (m *Payload) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_Payload.Unmarshal(m, b)
}
func (m *Payload) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_Payload.Marshal(b, m, deterministic)
}
func (m *Payload) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Payload.Merge(m, src)
}
func (m *Payload) XXX_Size() int {
	return xxx_messageInfo_Payload.Size(m)
}
func (m *Payload) XXX_DiscardUnknown() {
	xxx_messageInfo_Payload.DiscardUnknown(m)
}

var xxx_messageInfo_Payload proto.InternalMessageInfo

func (m *Payload) GetVersion() uint32 {
	if m != nil && m.Version != nil {
		return *m.Version
	}
	return 0
}

func (m *Payload) GetVbkeys() []*VbKeyVersions {
	if m != nil {
		return m.Vbkeys
	}
	return nil
}

func (m *Payload) GetVbmap() *VbConnectionMap {
	if m != nil {
		return m.Vbmap
	}
	return nil
}

// List of vbuckets that will be streamed via a newly opened connection.
type VbConnectionMap struct {
	Bucket               *string  `protobuf:"bytes,1,req,name=bucket" json:"bucket,omitempty"`
	Vbuckets             []uint32 `protobuf:"varint,3,rep,name=vbuckets" json:"vbuckets,omitempty"`
	Vbuuids              []uint64 `protobuf:"varint,4,rep,name=vbuuids" json:"vbuuids,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *VbConnectionMap) Reset()         { *m = VbConnectionMap{} }
func (m *VbConnectionMap) String() string { return proto.CompactTextString(m) }
func (*VbConnectionMap) ProtoMessage()    {}
func (*VbConnectionMap) Descriptor() ([]byte, []int) {
	return fileDescriptor_abb5a9726058e37d, []int{1}
}

func (m *VbConnectionMap) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_VbConnectionMap.Unmarshal(m, b)
}
func (m *VbConnectionMap) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_VbConnectionMap.Marshal(b, m, deterministic)
}
func (m *VbConnectionMap) XXX_Merge(src proto.Message) {
	xxx_messageInfo_VbConnectionMap.Merge(m, src)
}
func (m *VbConnectionMap) XXX_Size() int {
	return xxx_messageInfo_VbConnectionMap.Size(m)
}
func (m *VbConnectionMap) XXX_DiscardUnknown() {
	xxx_messageInfo_VbConnectionMap.DiscardUnknown(m)
}

var xxx_messageInfo_VbConnectionMap proto.InternalMessageInfo

func (m *VbConnectionMap) GetBucket() string {
	if m != nil && m.Bucket != nil {
		return *m.Bucket
	}
	return ""
}

func (m *VbConnectionMap) GetVbuckets() []uint32 {
	if m != nil {
		return m.Vbuckets
	}
	return nil
}

func (m *VbConnectionMap) GetVbuuids() []uint64 {
	if m != nil {
		return m.Vbuuids
	}
	return nil
}

type VbKeyVersions struct {
	Vbucket              *uint32           `protobuf:"varint,2,req,name=vbucket" json:"vbucket,omitempty"`
	Vbuuid               *uint64           `protobuf:"varint,3,req,name=vbuuid" json:"vbuuid,omitempty"`
	Bucketname           *string           `protobuf:"bytes,4,req,name=bucketname" json:"bucketname,omitempty"`
	Kvs                  []*KeyVersions    `protobuf:"bytes,5,rep,name=kvs" json:"kvs,omitempty"`
	ProjVer              *ProjectorVersion `protobuf:"varint,6,opt,name=projVer,enum=protobuf.ProjectorVersion" json:"projVer,omitempty"`
	Opaque2              *uint64           `protobuf:"varint,7,opt,name=opaque2" json:"opaque2,omitempty"`
	XXX_NoUnkeyedLiteral struct{}          `json:"-"`
	XXX_unrecognized     []byte            `json:"-"`
	XXX_sizecache        int32             `json:"-"`
}

func (m *VbKeyVersions) Reset()         { *m = VbKeyVersions{} }
func (m *VbKeyVersions) String() string { return proto.CompactTextString(m) }
func (*VbKeyVersions) ProtoMessage()    {}
func (*VbKeyVersions) Descriptor() ([]byte, []int) {
	return fileDescriptor_abb5a9726058e37d, []int{2}
}

func (m *VbKeyVersions) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_VbKeyVersions.Unmarshal(m, b)
}
func (m *VbKeyVersions) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_VbKeyVersions.Marshal(b, m, deterministic)
}
func (m *VbKeyVersions) XXX_Merge(src proto.Message) {
	xxx_messageInfo_VbKeyVersions.Merge(m, src)
}
func (m *VbKeyVersions) XXX_Size() int {
	return xxx_messageInfo_VbKeyVersions.Size(m)
}
func (m *VbKeyVersions) XXX_DiscardUnknown() {
	xxx_messageInfo_VbKeyVersions.DiscardUnknown(m)
}

var xxx_messageInfo_VbKeyVersions proto.InternalMessageInfo

func (m *VbKeyVersions) GetVbucket() uint32 {
	if m != nil && m.Vbucket != nil {
		return *m.Vbucket
	}
	return 0
}

func (m *VbKeyVersions) GetVbuuid() uint64 {
	if m != nil && m.Vbuuid != nil {
		return *m.Vbuuid
	}
	return 0
}

func (m *VbKeyVersions) GetBucketname() string {
	if m != nil && m.Bucketname != nil {
		return *m.Bucketname
	}
	return ""
}

func (m *VbKeyVersions) GetKvs() []*KeyVersions {
	if m != nil {
		return m.Kvs
	}
	return nil
}

func (m *VbKeyVersions) GetProjVer() ProjectorVersion {
	if m != nil && m.ProjVer != nil {
		return *m.ProjVer
	}
	return ProjectorVersion_V5_1_0
}

func (m *VbKeyVersions) GetOpaque2() uint64 {
	if m != nil && m.Opaque2 != nil {
		return *m.Opaque2
	}
	return 0
}

// mutations are broadly divided into data and control messages. The division
// is based on the commands.
//
// Interpreting seq.no:
// 1. For Upsert, Deletion, UpsertDeletion messages, sequence number corresponds
//    to kv mutation.
// 2. For Sync message, it is the latest kv mutation sequence-no. received for
//    a vbucket.
// 3. For DropData message, it is the first kv mutation that was dropped due
//    to buffer overflow.
// 4. For StreamBegin, it is zero.
// 5. For StreamEnd, it is the last kv mutation received before ending a vbucket
//    stream with kv.
//
// Interpreting Snapshot marker:
//    Key versions can contain snapshot-marker {start-seqno, end-seqno},
//    instead of using separate field for them, following fields are
//    mis-interpreted,
//      uuid   - type  (8 byte)
//      key    - start-seqno (8 byte)
//      oldkey - end-seqno (8 byte)
//
// fields `docid`, `uuids`, `keys`, `oldkeys` are valid only for
// Upsert, Deletion, UpsertDeletion messages.
type KeyVersions struct {
	Seqno                *uint64  `protobuf:"varint,1,req,name=seqno" json:"seqno,omitempty"`
	Docid                []byte   `protobuf:"bytes,2,opt,name=docid" json:"docid,omitempty"`
	Uuids                []uint64 `protobuf:"varint,3,rep,name=uuids" json:"uuids,omitempty"`
	Commands             []uint32 `protobuf:"varint,4,rep,name=commands" json:"commands,omitempty"`
	Keys                 [][]byte `protobuf:"bytes,5,rep,name=keys" json:"keys,omitempty"`
	Oldkeys              [][]byte `protobuf:"bytes,6,rep,name=oldkeys" json:"oldkeys,omitempty"`
	Partnkeys            [][]byte `protobuf:"bytes,7,rep,name=partnkeys" json:"partnkeys,omitempty"`
	PrjMovingAvg         *int64   `protobuf:"varint,8,opt,name=prjMovingAvg" json:"prjMovingAvg,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *KeyVersions) Reset()         { *m = KeyVersions{} }
func (m *KeyVersions) String() string { return proto.CompactTextString(m) }
func (*KeyVersions) ProtoMessage()    {}
func (*KeyVersions) Descriptor() ([]byte, []int) {
	return fileDescriptor_abb5a9726058e37d, []int{3}
}

func (m *KeyVersions) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_KeyVersions.Unmarshal(m, b)
}
func (m *KeyVersions) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_KeyVersions.Marshal(b, m, deterministic)
}
func (m *KeyVersions) XXX_Merge(src proto.Message) {
	xxx_messageInfo_KeyVersions.Merge(m, src)
}
func (m *KeyVersions) XXX_Size() int {
	return xxx_messageInfo_KeyVersions.Size(m)
}
func (m *KeyVersions) XXX_DiscardUnknown() {
	xxx_messageInfo_KeyVersions.DiscardUnknown(m)
}

var xxx_messageInfo_KeyVersions proto.InternalMessageInfo

func (m *KeyVersions) GetSeqno() uint64 {
	if m != nil && m.Seqno != nil {
		return *m.Seqno
	}
	return 0
}

func (m *KeyVersions) GetDocid() []byte {
	if m != nil {
		return m.Docid
	}
	return nil
}

func (m *KeyVersions) GetUuids() []uint64 {
	if m != nil {
		return m.Uuids
	}
	return nil
}

func (m *KeyVersions) GetCommands() []uint32 {
	if m != nil {
		return m.Commands
	}
	return nil
}

func (m *KeyVersions) GetKeys() [][]byte {
	if m != nil {
		return m.Keys
	}
	return nil
}

func (m *KeyVersions) GetOldkeys() [][]byte {
	if m != nil {
		return m.Oldkeys
	}
	return nil
}

func (m *KeyVersions) GetPartnkeys() [][]byte {
	if m != nil {
		return m.Partnkeys
	}
	return nil
}

func (m *KeyVersions) GetPrjMovingAvg() int64 {
	if m != nil && m.PrjMovingAvg != nil {
		return *m.PrjMovingAvg
	}
	return 0
}

func init() {
	proto.RegisterEnum("protobuf.Command", Command_name, Command_value)
	proto.RegisterEnum("protobuf.ProjectorVersion", ProjectorVersion_name, ProjectorVersion_value)
	proto.RegisterType((*Payload)(nil), "protobuf.Payload")
	proto.RegisterType((*VbConnectionMap)(nil), "protobuf.VbConnectionMap")
	proto.RegisterType((*VbKeyVersions)(nil), "protobuf.VbKeyVersions")
	proto.RegisterType((*KeyVersions)(nil), "protobuf.KeyVersions")
}

func init() { proto.RegisterFile("mutation.proto", fileDescriptor_abb5a9726058e37d) }

var fileDescriptor_abb5a9726058e37d = []byte{
	// 456 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x64, 0x91, 0xcd, 0x8e, 0xd3, 0x30,
	0x14, 0x85, 0x95, 0xd8, 0x4d, 0xda, 0xdb, 0xa6, 0x35, 0x16, 0x08, 0xc3, 0xca, 0xea, 0x06, 0x6b,
	0x90, 0x2a, 0x5a, 0xa9, 0xec, 0x99, 0x29, 0x2b, 0x34, 0xd2, 0x48, 0x23, 0xba, 0xad, 0x9c, 0xc4,
	0x54, 0x69, 0x1b, 0x3b, 0xe3, 0xfc, 0x48, 0x7d, 0x0f, 0x9e, 0x81, 0xe7, 0x44, 0x76, 0xca, 0x4c,
	0x81, 0x95, 0xaf, 0x3f, 0x1f, 0xdf, 0x7b, 0x8e, 0x0d, 0xd3, 0xb2, 0x6d, 0x64, 0x53, 0x18, 0xbd,
	0xa8, 0xac, 0x69, 0x0c, 0x1d, 0xfa, 0x25, 0x6d, 0x7f, 0xcc, 0x4b, 0x88, 0x1f, 0xe4, 0xf9, 0x64,
	0x64, 0x4e, 0x67, 0x10, 0x77, 0xca, 0xd6, 0x85, 0xd1, 0x2c, 0xe0, 0xa1, 0x48, 0xe8, 0x07, 0x88,
	0xba, 0xf4, 0xa8, 0xce, 0x35, 0x0b, 0x39, 0x12, 0xe3, 0xd5, 0xdb, 0xc5, 0x9f, 0x6b, 0x8b, 0x6d,
	0xfa, 0x4d, 0x9d, 0xb7, 0xbd, 0xba, 0xa6, 0x02, 0x06, 0x5d, 0x5a, 0xca, 0x8a, 0x21, 0x1e, 0x88,
	0xf1, 0xea, 0xdd, 0xb5, 0xee, 0xce, 0x68, 0xad, 0x32, 0x37, 0xfb, 0x5e, 0x56, 0xf3, 0x0d, 0xcc,
	0xfe, 0x41, 0x74, 0x0a, 0x51, 0xda, 0x66, 0x47, 0xd5, 0xf8, 0xa9, 0x23, 0x4a, 0x60, 0xd8, 0xf5,
	0xa0, 0x66, 0x88, 0x23, 0x91, 0x78, 0x63, 0x69, 0xdb, 0x16, 0x79, 0xcd, 0x30, 0x47, 0x02, 0xcf,
	0x7f, 0x05, 0x90, 0xfc, 0xed, 0xa0, 0x97, 0xf8, 0x2e, 0xa1, 0xf7, 0x3e, 0x75, 0xde, 0xdd, 0x1d,
	0x86, 0x78, 0x28, 0x30, 0xa5, 0x00, 0xfd, 0xb9, 0x96, 0xa5, 0x62, 0xd8, 0x4f, 0x9a, 0x03, 0x3a,
	0x76, 0x35, 0x1b, 0xf8, 0x70, 0x6f, 0x5e, 0x4c, 0x5f, 0x37, 0xfe, 0x08, 0x71, 0x65, 0xcd, 0x61,
	0xab, 0x2c, 0x8b, 0x78, 0x20, 0xa6, 0xab, 0xf7, 0x2f, 0xba, 0x07, 0x6b, 0x0e, 0x2a, 0x6b, 0x8c,
	0xbd, 0xa8, 0x9d, 0x0b, 0x53, 0xc9, 0xa7, 0x56, 0xad, 0x58, 0xcc, 0x03, 0x81, 0xe7, 0x3f, 0x03,
	0x18, 0x5f, 0x77, 0x4b, 0x60, 0x50, 0xab, 0x27, 0x6d, 0x7c, 0x54, 0xec, 0xb6, 0xb9, 0xc9, 0x8a,
	0x9c, 0x85, 0x3c, 0x10, 0x13, 0xb7, 0xed, 0x53, 0xba, 0xd8, 0xd8, 0x3d, 0x44, 0x66, 0xca, 0x52,
	0xea, 0x4b, 0xee, 0x84, 0x4e, 0x00, 0xfb, 0xef, 0x70, 0x8e, 0x27, 0x7e, 0xda, 0x29, 0xf7, 0x20,
	0xf2, 0xe0, 0x15, 0x8c, 0x2a, 0x69, 0x1b, 0xed, 0x51, 0xec, 0xd1, 0x6b, 0x98, 0x54, 0xf6, 0x70,
	0x6f, 0xba, 0x42, 0xef, 0xbf, 0x74, 0x7b, 0x36, 0xe4, 0x81, 0x40, 0x37, 0x06, 0xe2, 0xbb, 0xbe,
	0x33, 0x05, 0x88, 0xbe, 0x57, 0xb5, 0xb2, 0x0d, 0x09, 0xe8, 0x04, 0x86, 0x1b, 0x75, 0x52, 0xee,
	0x63, 0x48, 0x48, 0x29, 0x4c, 0xfb, 0x93, 0x67, 0x86, 0xe8, 0x10, 0xf0, 0xe3, 0x59, 0x67, 0x04,
	0x7b, 0xad, 0x35, 0xd5, 0x46, 0x36, 0x92, 0x0c, 0xe8, 0x0c, 0xc6, 0x8f, 0x8d, 0x55, 0xb2, 0xbc,
	0x55, 0xfb, 0x42, 0x93, 0x88, 0x26, 0x30, 0xea, 0xc1, 0x57, 0x9d, 0x93, 0xf8, 0xe6, 0x16, 0xc8,
	0x7f, 0x8f, 0x05, 0x10, 0x6d, 0xd7, 0xbb, 0xe5, 0xee, 0x13, 0x09, 0x9e, 0xeb, 0x25, 0x09, 0x2f,
	0xf5, 0x7a, 0xb7, 0x24, 0xe8, 0x52, 0x7f, 0xde, 0xad, 0x09, 0xfe, 0x1d, 0x00, 0x00, 0xff, 0xff,
	0x53, 0xbb, 0x66, 0x31, 0xc4, 0x02, 0x00, 0x00,
}
