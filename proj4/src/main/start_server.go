	package main

import(
	//"net/http"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"runtime"
	"strconv"

	// our lib
	"kvlib"
	//. "paxos"
	"kvpaxos"
)

var (
	role = kvlib.Det_role()
)

func RPC_Addr(me int, conf map[string]string) string {
	id := fmt.Sprintf("n%02d", me+1)
	ip,ok := conf[id]
		if !ok {
			fmt.Println("Failed to find IP :"+id);
			panic(conf)
		}

	if conf["use_different_port"] == "true" {

		p,err := strconv.Atoi(conf["RPC_port_"+id])
			if err != nil {
				fmt.Printf("Failed to parse : RPC_port_%s : %s\n",id, conf["RPC_port_"+id]);
				panic(err)
			}
		return ip+":"+strconv.Itoa(p)

	}
	p,err := strconv.Atoi(conf["RPCport"])
	if err != nil {
		println("Failed to parse conf[port]")
		panic(err)
	}
	return ip+":"+strconv.Itoa(p)

}

func usage(){
	fmt.Println("Usage: bin/start_server <n01|n02|...>")
	os.Exit(1)
}

func main(){
  kvpaxos.RPC_Use_TCP = 1
	runtime.GOMAXPROCS(4)
	const nservers = 3
	var kva []*kvpaxos.KVPaxos = make([]*kvpaxos.KVPaxos, nservers)
	var kvh []string = make([]string, nservers)

	conf:=kvlib.ReadJson("conf/settings.conf")

	for i := 0; i < nservers; i++ {
		kvh[i] = RPC_Addr(i,conf)
	}

	stop := make(chan os.Signal)
	signal.Notify(stop, syscall.SIGINT)

	if len(os.Args)>1 {
		var kva_me *kvpaxos.KVPaxos
		if role<0{
			usage()
		}else{
			kva_me = kvpaxos.StartServer(kvh, role-1)
			fmt.Printf("Serving HTTP, Server ID: %d\n", role)
		}
		select {
			case signal := <-stop:
				fmt.Printf("Got signal:%v\n", signal)
				kva_me.Kill()
		}

	}else{
		for i := 0; i < nservers; i++ {
			kva[i] = kvpaxos.StartServer(kvh, i)
		}
		fmt.Printf("Serving HTTP\n")
		select {
			case signal := <-stop:
				fmt.Printf("Got signal:%v\n", signal)

			for i := 0; i < nservers; i++ {
				kva[i].Kill()
			}
		}
	}
}
