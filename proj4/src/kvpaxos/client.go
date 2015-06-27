package kvpaxos

import "net/rpc"
import "fmt"

import "math/rand"
import "time"

type Clerk struct {
  servers []string
  // You will have to modify this struct.
  myID int
  opCnt int
}
var ck_cnt=10

func MakeClerk(servers []string) *Clerk {
  ck := new(Clerk)
  ck.servers = servers
  // You'll have to add code here.
  // seed for random client id and operation id
  //time.Sleep(11)
  //rand.Seed( time.Now().UTC().UnixNano())
  //ck.myID = rand.Int()
  ck.myID=ck_cnt
  ck_cnt+=1
  ck.opCnt=0
    // fmt.Printf("Client %d created\n",ck.myID);
  return ck
}

//
// call() sends an RPC to the rpcname handler on server srv
// with arguments args, waits for the reply, and leaves the
// reply in reply. the reply argument should be a pointer
// to a reply structure.
//
// the return value is true if the server responded, and false
// if call() was not able to contact the server. in particular,
// the reply's contents are only valid if call() returned true.
//
// you should assume that call() will time out and return an
// error after a while if it doesn't get a reply from the server.
//
// please use call() to send all RPCs, in client.go and server.go.
// please don't change this function.
//
func call(srv string, rpcname string,
          args interface{}, reply interface{}) bool {
  c, errx := rpc.Dial("unix", srv)
  if errx != nil {
    return false
  }
  defer c.Close()
    
  err := c.Call(rpcname, args, reply)
  if err == nil {
    return true
  }

  fmt.Println(err)
  return false
}

//
// fetch the current value for a key.
// returns "" if the key does not exist.
// keeps trying forever in the face of all other errors.
//
func (ck *Clerk) Get(key string) string {
  // You will have to modify this function.
  var args GetArgs
  var reply GetReply
  //args = GetArgs{Key:key, OpID:rand.Int(), ClientID:ck.myID}
  args = GetArgs{Key:key, OpID:ck.opCnt+ck.myID*10000, ClientID:ck.myID}
  ck.opCnt+=1
  ok := false
  for !ok {
    //server := 0 // always pick the first server for debuggin purpose
    server := rand.Int() % len(ck.servers)
    ok = call(ck.servers[server], "KVPaxos.Get", args, &reply)
    time.Sleep(time.Millisecond) // sleep one second before next call
    // if ok {
    //   fmt.Println("Done")
    // } else {
    //   fmt.Println("Fail")
    // }
  }
  return reply.Value
}

//
// set the value for a key.
// keeps trying until it succeeds.
//
func (ck *Clerk) PutExt(key string, value string, dohash bool) string {
  // You will have to modify this function.
  var args PutArgs
  var reply PutReply
  //args = PutArgs{Key:key, Value:value, DoHash:dohash, OpID:rand.Int(), ClientID:ck.myID}
  args = PutArgs{Key:key, Value:value, DoHash:dohash, OpID:ck.opCnt+ck.myID*10000, ClientID:ck.myID}
  ck.opCnt+=1
  ok := false
  for !ok {
    //server := 0 // always pick the first server for debuggin purpose
    server := rand.Int() % len(ck.servers)
    ok = call(ck.servers[server], "KVPaxos.Put", args, &reply)
    time.Sleep(time.Millisecond*5) // sleep one second before next call
    // if ok {
    //   fmt.Println("Done")
    // } else {
    //   fmt.Println("Fail")
    // }
  }
  return reply.PreviousValue
}

func (ck *Clerk) Put(key string, value string) {
  ck.PutExt(key, value, false)
}
func (ck *Clerk) PutReturn(key string, value string) string {
  v := ck.PutExt(key, value, false)
  return v
}
