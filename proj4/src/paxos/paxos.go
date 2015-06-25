package paxos

//
// Paxos library, to be included in an application.
// Multiple applications will run, each including
// a Paxos peer.
//
// Manages a sequence of agreed-on values.
// The set of peers is fixed.
// Copes with network failures (partition, msg loss, &c).
// Does not store anything persistently, so cannot handle crash+restart.
//
// The application interface:
//
// px = paxos.Make(peers []string, me string)
// px.Start(seq int, v interface{}) -- start agreement on new instance
// px.Status(seq int) (decided bool, v interface{}) -- get info about an instance
// px.Done(seq int) -- ok to forget all instances <= seq
// px.Max() int -- highest instance seq known, or -1
// px.Min() int -- instances before this seq have been forgotten
//

import "net"
import "net/rpc"
import "log"
import "os"
import "syscall"
import "sync"
import "fmt"
import "math/rand"
import "time"

const (
  REJECT = "reject"
  ACCEPT = "accept"
)

// debug message setting
const (
  DEBUG     = true
  DEBUG_INI = false
  DEBUG_PRE = true
  DEBUG_ACC = true
  DEBUG_DEC = true
  DEBUG_ARG = false
)

type Paxos struct {
  mu sync.Mutex // lock the paxos server
  l net.Listener
  dead bool
  unreliable bool
  rpcCount int
  peers []string
  me int // index into peers[]


  // Your data here.
  instances map[int]PaxosInstance //active paxos instances
  majority int // the number what majority means (# of server)/2 + 1
  dones []int
}

type PaxosProposal struct{
  Value interface{}
  PaxosNum int
}

func (p *PaxosProposal) toString() string{
  return fmt.Sprintf("Proposal(Number:%d, Value:%v)", p.PaxosNum, p.Value)
}

type PaxosReply struct {
  State string
  Proposal PaxosProposal
}

func (p *PaxosReply) toString() string{
  ret := "Reply(State:"
  if p.State == ACCEPT {
    ret += "ACCEPT"
  }else if p.State == REJECT{
    ret += "REJECT"
  }
  ret += ", " + p.Proposal.toString() + ")"
  return ret
}

type PaxosInstance struct {
  decided  bool // is consensus reached?
  maxPrepareNum int // the highest seen paxos number
  acceptedProposal PaxosProposal // accepted proposal
}

func (p *PaxosInstance) toString() string{
  ret := fmt.Sprintf("Instance{Max Number:%d," , p.maxPrepareNum )
  if p.decided {
    ret += "Decided "
  } else { 
    ret += "Accepted "
  }
  ret += p.acceptedProposal.toString()
  return ret
}

// args struct for RPC
type PaxosArgs struct {
  Seq int
  Proposal PaxosProposal
}

func (p *PaxosArgs) toString() string{
  return fmt.Sprintf("Args(seq:%d,%s) ", p.Seq, p.Proposal.toString() )
}


func Assert(condition bool, msg string){
  if !condition {
    fmt.Printf("ASSERT: %s\n",msg);
    os.Exit(-1)
  }
}

// handle exist
func (px *Paxos) MakePaxosInstance(seq int) {
  px.mu.Lock(); // Protect px.instances
  defer px.mu.Unlock();
  if _, exists := px.instances[seq]; !exists {
    if DEBUG && DEBUG_INI{
      fmt.Printf("%d create new paxos instance[%d]\n",px.me ,seq) 
    }
    px.instances[seq] = PaxosInstance{decided: false, maxPrepareNum: -1, acceptedProposal: PaxosProposal{PaxosNum: -1, Value: nil}}
    // handle max?
  }
}

//
// call() sends an RPC to the rpcname handler on server srv
// with arguments args, waits for the reply, and leaves the
// reply in reply. the reply argument should be a pointer
// to a reply structure.
//
// the return value is true if the server responded, and false
// if call() was not able to contact the server. in particular,
// the replys contents are only valid if call() returned true.
//
// you should assume that call() will time out and return an
// error after a while if it does not get a reply from the server.
//
// please use call() to send all RPCs, in client.go and server.go.
// please do not change this function.
//
func call(srv string, name string, args interface{}, reply interface{}) bool {
  c, err := rpc.Dial("unix", srv)
  if err != nil {
    err1 := err.(*net.OpError)
    if err1.Err != syscall.ENOENT && err1.Err != syscall.ECONNREFUSED {
      fmt.Printf("paxos Dial() failed: %v\n", err1)
    }
    return false
  }
  defer c.Close()
    
  err = c.Call(name, args, reply)
  if err == nil {
    return true
  }

  fmt.Println(err)
  return false
}


//
// the application wants paxos to start agreement on
// instance seq, with proposed value v.
// Start() returns right away; the application will
// call Status() to find out if/when agreement
// is reached.
//
func (px *Paxos) Start(seq int, v interface{}) {
  // Your code here.
  go func() {
    // px.Min() return the minimum valid seq number, so >=, not >
    time.Sleep(time.Duration(rand.Intn(10)*100))
    if seq >= px.Min(){
      // Create if not exist
      px.MakePaxosInstance(seq)

      for !px.dead { 
        // Generate Paxos Number 
        px.mu.Lock() // protect px.instances[seq].maxPrepareNum
        paxosNum := px.instances[seq].maxPrepareNum + rand.Intn(len(px.peers)) + 1
        px.mu.Unlock()
        
        // Prepare phase
        isAccept, replyProposal := px.sendPrepare(seq, paxosNum)

        // Accept phase
        // Replace the paxos number accpeted proposal to new paxosNum
        replyProposal.PaxosNum = paxosNum 
        if replyProposal.PaxosNum == -1 {
          replyProposal.Value = v
        }
        if isAccept {
          isAccept = px.sendAccept(seq, replyProposal)
        }
        // Decide phase
        if isAccept {
          px.sendDecide(seq, replyProposal)
          break;
        }
      }
    }
  }()
}

// does not need proposal in prepare phase
func (px *Paxos) sendPrepare(seq int, paxosNum int) (bool, PaxosProposal){

  proposal := PaxosProposal{PaxosNum: paxosNum, Value: nil} // initialize
  args := PaxosArgs{Seq: seq, Proposal: proposal}
  replyProposal := PaxosProposal{PaxosNum: -1, Value: nil} // initialize
  reply := PaxosReply{State:REJECT}
  replyNum := 0

  for index, acceptor := range px.peers {
    if DEBUG && DEBUG_PRE {
      fmt.Printf("%d send prepare(%d) to %d\n", px.me ,paxosNum, index);
    }

    isAccept := false
    if index == px.me{
      px.HandlePrepare(&args, &reply)
      isAccept = (reply.State == ACCEPT)
    }else{
      isAccept = call(acceptor, "Paxos.HandlePrepare", &args, &reply) // true = get reply
      if isAccept {
        isAccept = (reply.State == ACCEPT) // true = accept
      }
    }
  
    if isAccept {
      if DEBUG && DEBUG_PRE{
        fmt.Printf("%d accept prepare number %d from %d\n", index, paxosNum ,px.me )
      }
      replyNum++
      // get the proposal with largest paxos number
      if reply.Proposal.PaxosNum > replyProposal.PaxosNum{
        replyProposal = reply.Proposal
      }
    } else {
      if DEBUG && DEBUG_PRE{
        fmt.Printf("%d reject prepare number %d from %d\n", index, paxosNum ,px.me )
      }
    }
  }
  // update max paxosnum?
  return replyNum >= px.majority, replyProposal
}

// It is RPC
func (px *Paxos) HandlePrepare(args *PaxosArgs, reply *PaxosReply) error {
  if DEBUG && DEBUG_ARG {
    fmt.Printf("%d HandlePrepare %s\n", px.me, args.toString());
  }
  proposal := args.Proposal
  seq := args.Seq
  reply.State = REJECT

  // Create if not exist
  px.MakePaxosInstance(seq)

  px.mu.Lock() // protect px.instances[seq].maxPrepareNum
  defer px.mu.Unlock()
  if proposal.PaxosNum > px.instances[seq].maxPrepareNum {
    // px.instances[seq].maxPrepareNum = proposal.paxosNum
    obj := px.instances[seq]
    obj.maxPrepareNum = proposal.PaxosNum
    px.instances[seq] = obj
    reply.Proposal = px.instances[seq].acceptedProposal
    reply.State = ACCEPT
  }

  return nil
}

func (px *Paxos) sendAccept(seq int, proposal PaxosProposal) (bool){


  args := PaxosArgs{Seq: seq, Proposal: proposal}
  reply := PaxosReply{State:REJECT}
  replyNum := 0

  for index, acceptor := range px.peers {
    if DEBUG && DEBUG_ACC{
      fmt.Printf("%d send accept(%s) to %d\n",px.me ,proposal.toString(), index);
    }
    isAccept := false
    if index == px.me{
      px.HandleAccept(&args, &reply)
      isAccept = (reply.State == ACCEPT)
    }else{
      isAccept = call(acceptor, "Paxos.HandleAccept", &args, &reply) // true = get reply
      if isAccept {
        isAccept = (reply.State == ACCEPT)
      }
    }
    if isAccept {
      if DEBUG && DEBUG_ACC{
        fmt.Printf("%d accept %s from %d\n", index, proposal.toString() ,px.me )
      }
      replyNum++
    } else {
      if DEBUG && DEBUG_ACC{
        fmt.Printf("%d reject %s from %d\n", index, proposal.toString() ,px.me )
      }
    }
  }
  return replyNum >= px.majority
}

func (px *Paxos) HandleAccept(args *PaxosArgs, reply *PaxosReply) error {
  seq := args.Seq
  proposal := args.Proposal
  reply.State = REJECT

  // Create if not exist
  px.MakePaxosInstance(seq)

  px.mu.Lock()
  defer px.mu.Unlock()
  obj := px.instances[seq]
  if ((proposal.PaxosNum >= obj.maxPrepareNum) && (proposal.PaxosNum > obj.acceptedProposal.PaxosNum)) {
    obj.acceptedProposal = proposal
    obj.maxPrepareNum = proposal.PaxosNum
    px.instances[seq] = obj
    reply.State = ACCEPT
  }
  return nil
}

func (px *Paxos) sendDecide(seq int, proposal PaxosProposal) {
  
  args := PaxosArgs{Seq: seq, Proposal: proposal}
  reply := PaxosReply{State:REJECT}

  for index, acceptor := range px.peers {
    if DEBUG && DEBUG_DEC{
      fmt.Printf("%d send decide(%s) to %d\n",px.me ,proposal.toString(), index);
    }
    isAccept := false
    if index == px.me{
      px.HandleDecide(&args, &reply)
      isAccept = (reply.State == ACCEPT)
    }else{
      isAccept = call(acceptor, "Paxos.HandleDecide", &args, &reply) // true = get reply
      if isAccept {
        if DEBUG && DEBUG_ACC{
          fmt.Printf("%d decided %s on instance %d\n", index, proposal.toString() , seq )
        }
        isAccept = (reply.State == ACCEPT) // true = accept
      }
    }

    if isAccept {
      if DEBUG && DEBUG_ACC{
        fmt.Printf("%d decided %s on instance %d\n", index, proposal.toString() , seq )
      }
    }

  }
}

func (px *Paxos) HandleDecide(args *PaxosArgs, reply *PaxosReply) error {
  
  seq := args.Seq
  proposal := args.Proposal
  reply.State = REJECT
  
  // Create if not exist
  px.MakePaxosInstance(seq)

  px.mu.Lock()
  defer px.mu.Unlock()

  // px.instances[seq].acceptedProposal = proposal
  // px.instances[seq].decided = true
  obj := px.instances[seq]
  obj.acceptedProposal = proposal
  obj.decided = true
  px.instances[seq] = obj
  reply.State = ACCEPT


  // Update Done 
  me := px.me
  if px.dones[me] < seq {
    px.dones[me] = seq
  }
  return nil
}

//
// the application on this machine is done with
// all instances <= seq.
//
// see the comments for Min() for more explanation.
//
func (px *Paxos) Done(seq int) {
  // Your code here.
  if px.dones[px.me] < seq {
    px.dones[px.me] = seq
  }
}

//
// the application wants to know the
// highest instance sequence known to
// this peer.
//
func (px *Paxos) Max() int {
  // Your code here.
  max := -1
  for num := range px.instances {
    if num > max{
      max = num
    }
  }
  return max
}

//
// Min() should return one more than the minimum among z_i,
// where z_i is the highest number ever passed
// to Done() on peer i. A peers z_i is -1 if it has
// never called Done().
//
// Paxos is required to have forgotten all information
// about any instances it knows that are < Min().
// The point is to free up memory in long-running
// Paxos-based servers.
//
// Paxos peers need to exchange their highest Done()
// arguments in order to implement Min(). These
// exchanges can be piggybacked on ordinary Paxos
// agreement protocol messages, so it is OK if one
// peers Min does not reflect another Peers Done()
// until after the next instance is agreed to.
//
// The fact that Min() is defined as a minimum over
// *all* Paxos peers means that Min() cannot increase until
// all peers have been heard from. So if a peer is dead
// or unreachable, other peers Min()s will not increase
// even if all reachable peers call Done. The reason for
// this is that when the unreachable peer comes back to
// life, it will need to catch up on instances that it
// missed -- the other peers therefor cannot forget these
// instances.
// 
func (px *Paxos) Min() int {
  // You code here.
  //tmp
  px.mu.Lock()
  defer px.mu.Unlock()
  min := px.dones[px.me]
  for k := range px.dones {
    if min > px.dones[k] {
      min = px.dones[k]
    }
  }

  for k := range px.instances{
    if k <= min && px.instances[k].decided {
      delete(px.instances, k)
    }
  }

  return min + 1
}

//
// the application wants to know whether this
// peer thinks an instance has been decided,
// and if so what the agreed value is. Status()
// should just inspect the local peer state;
// it should not contact other Paxos peers.
//
func (px *Paxos) Status(seq int) (bool, interface{}) {
  // Your code here.

  min := px.Min()
  if seq < min {
    return false, nil
  }
  px.mu.Lock()
  defer px.mu.Unlock()
  if _, exists := px.instances[seq]; !exists {
    return false, nil
  }
  return px.instances[seq].decided, px.instances[seq].acceptedProposal.Value
}


//
// tell the peer to shut itself down.
// for testing.
// please do not change this function.
//
func (px *Paxos) Kill() {
  px.dead = true
  if px.l != nil {
    px.l.Close()
  }
}

//
// the application wants to create a paxos peer.
// the ports of all the paxos peers (including this one)
// are in peers[]. this servers port is peers[me].
//
func Make(peers []string, me int, rpcs *rpc.Server) *Paxos {
  px := &Paxos{}
  px.peers = peers
  px.me = me


  // Your initialization code here.
  px.majority = len(peers)/2+1
  px.instances = map[int]PaxosInstance{}
  px.dones = make([]int, len(peers))
  for i := range peers {
    px.dones[i] = -1
  }
  if DEBUG && DEBUG_INI{
    fmt.Printf("%d majority: %d/%d\n",px.me, px.majority, len(peers)) 
  }
  // End of initialization code

  if rpcs != nil {
    // caller will create socket &c
    rpcs.Register(px)
  } else {
    rpcs = rpc.NewServer()
    rpcs.Register(px)

    // prepare to receive connections from clients.
    // change "unix" to "tcp" to use over a network.
    os.Remove(peers[me]) // only needed for "unix"
    l, e := net.Listen("unix", peers[me]);
    if e != nil {
      log.Fatal("listen error: ", e);
    }
    px.l = l
    
    // please do not change any of the following code,
    // or do anything to subvert it.
    
    // create a thread to accept RPC connections
    go func() {
      for px.dead == false {
        conn, err := px.l.Accept()
        if err == nil && px.dead == false {
          if px.unreliable && (rand.Int63() % 1000) < 100 {
            // discard the request.
            conn.Close()
          } else if px.unreliable && (rand.Int63() % 1000) < 200 {
            // process the request but force discard of reply.
            c1 := conn.(*net.UnixConn)
            f, _ := c1.File()
            err := syscall.Shutdown(int(f.Fd()), syscall.SHUT_WR)
            if err != nil {
              fmt.Printf("shutdown: %v\n", err)
            }
            px.rpcCount++
            go rpcs.ServeConn(conn)
          } else {
            px.rpcCount++
            go rpcs.ServeConn(conn)
          }
        } else if err == nil {
          conn.Close()
        }
        if err != nil && px.dead == false {
          fmt.Printf("Paxos(%v) accept: %v\n", me, err.Error())
        }
      }
    }()
  }


  return px
}
