package main

import (
  "net"
  "net/url"
  "net/http"
  "html"
  "log"
  "time"
  "fmt"
  "strconv"
  "os"
  "os/exec"
  "io/ioutil"
  "crypto/md5"
  "encoding/hex"
  "encoding/json"
  "./cmap_string_string"
  )

func str_MD5(text string) string {
   hash := md5.Sum([]byte(text))
   return hex.EncodeToString(hash[:])
}
func MD5(text []byte) string {
   hash := md5.Sum(text)
   return hex.EncodeToString(hash[:])
}
  
func readConf(s string) map[string]string{
    dat, err := ioutil.ReadFile(s)
	if err != nil {
        panic(err)
    }
    //fmt.Println(dat)
	var udat map[string]interface{}
	if err := json.Unmarshal(dat, &udat); err != nil {
        panic(err)
    }
	ret:=make(map[string]string)
	
	for idx, val := range udat {
		var str=val.(string)
		ret[idx]=str
    }
    //fmt.Println("parsed config:")
	//fmt.Println(ret)
	return ret
}

const(
 PRIMARY=1
 BACKUP=2
)
const(
 COLD_START=0
 WARM_START=1
 BOOTSTRAP=2
 SYNC=3
 SHUTTING_DOWN=-1
)

func det_role() int {
	arg_num := len(os.Args)
	for i := 0 ; i < arg_num ;i++{
		switch os.Args[i] {
			case "-p": 
				return PRIMARY
			case "-b": 
				return BACKUP
		}
	}
	return 0
}
func find_port() (int,int,int){
	var p,err=strconv.Atoi(conf["port"])
		if err!=nil {
			fmt.Println("Failed to parse port:"+conf["port"]);
			panic(err)
		}
	var bp,err2=strconv.Atoi(conf["back_port"])
		if err!=nil{
			fmt.Println("Failed to parse back_port:"+conf["back_port"]);
			panic(err2)
		}
		
	if conf["primary"]!=conf["backup"]{
		return p,p,p
	}
	
	if role==PRIMARY{
		return p,p,bp
	}
	return bp,p,bp
}
func find_URL() (string,string,string){
	prim := "http://"+conf["primary"]+":"+strconv.Itoa(primaryPort)+"/kvman/"
	back := "http://"+conf["backup"]+":"+strconv.Itoa(backupPort)+"/kvman/"
	if role==PRIMARY{
		return back,prim,back
	}
	return prim,prim,back
}
var(
 role = det_role() //PRIMARY, SECONDARY
 stage = COLD_START // COLD_START=0 WARM_START=1 BOOTSTRAP=2 SYNC=3; SHUTTING_DOWN=-1
 conf = readConf("conf/settings.conf")
 listenPort, primaryPort, backupPort = find_port()
 peerURL, primaryURL, backupURL = find_URL()
 db = cmap_string_string.New()
 )
 
var peerSyncErrorSignal=make(chan int) //should we use buffered channel? or let all error'd process block and respond simultaneously?
var peerShutdownSignal=make(chan int)
var peerStartupSignal=make(chan int)
var peerInSyncSignal=make(chan int) 
 
func housekeeper(){
	for {		
		time.Sleep(5*time.Millisecond)
		msg :="DB server, role:"+strconv.Itoa(role)+"  stage:"+strconv.Itoa(stage)
		fmt.Println(msg)
		cmd := exec.Command("title", msg)
		_= cmd.Start()
		
		switch stage{
			case COLD_START:
				fmt.Println("Cold Start...")
				select{//when waiting as a primary...
					case _=<-peerSyncErrorSignal ://??
					case _=<-peerShutdownSignal : stage=BOOTSTRAP //I have priority
					case _=<-peerStartupSignal :  
						if role==PRIMARY{
							stage=BOOTSTRAP
						}else {
							stage=WARM_START
						}
					//primary have priority,go to bootstrap
					//secondary should go to warm start
					case _=<-peerInSyncSignal : stage=SYNC //alright, empty database is in sync
					default : 
				}
				//test if peer exist
				//if so, go to warm-start
				resp, err := fastClient.Get(peerURL+"peerstartup")
				if err==nil {
					defer resp.Body.Close()
					body, err2 := ioutil.ReadAll(resp.Body)
					if err2==nil && string(body)=="1"{	//good
						stage=WARM_START
						continue
					}
				}
				//peer doesn't exist; will be started later
				if role==PRIMARY {
					stage=BOOTSTRAP
				}
				//primary:continue backup: ->bootstrap
			case WARM_START:
				select{
					case _=<-peerSyncErrorSignal ://
						//shouldn't have write operations during warm-start. 
						//stay here!
					case _=<-peerShutdownSignal:
						continue
						//!!!! could be a test case.
					case _=<-peerStartupSignal : 
					if role==PRIMARY{
							stage=BOOTSTRAP
						}else {
							stage=WARM_START
						}
					//the other server starting up; I need to jump to bootstrap... only if i'm primary. (neither have data, then primary can cross the border line WARM|BOOTSTRAP)
					case _=<-peerInSyncSignal : stage=SYNC//What the heck? always do this...
					default : 
				}
				//fetch from peer
				//update db
				//send sync_start request "/kvman/peerstartsync?hash="
				//if success, go to SYNC; else, continue
				//if any error, start over
				resp1, err := http.Get(peerURL+"dump")
				if err!=nil {continue}
				defer resp1.Body.Close()
				body1, err2 := ioutil.ReadAll(resp1.Body)
				if err2!=nil {continue}
				
				db = cmap_string_string.New()//this is important; could improve performance though
				errM:=db.UnmarshalJSON([]byte(body1))
				if errM!=nil {
					fmt.Println("Unmarshall failure:"+string(body1))
					continue
				}
				str,_:=db.MarshalJSON();
				resp2, err3 := http.Get(peerURL+"peerstartsync?hash="+MD5(str))
				if err3!=nil {continue}
				defer resp2.Body.Close()
				body2, err4 := ioutil.ReadAll(resp2.Body)
				if err4!=nil {continue}
				if string(body2)==MD5(str){	//sync integrity check passed!
					stage=SYNC
					continue
				}	
			case BOOTSTRAP:
				select{
					case _=<-peerSyncErrorSignal :
						fmt.Println("BOOTSTRAP:  more peerSyncErrorSignal")
						for _ = range peerSyncErrorSignal {//flush the channel
						}
					//No one is having sync error when bootstrapping (db read-only); may be old errors from SYNC state
					case _=<-peerShutdownSignal ://stay here!
					case _=<-peerStartupSignal ://perhaps the starup failed, will start over
					case _=<-peerInSyncSignal : stage=SYNC //good
					default : 
				}
				//be patient; peer will do kvman/dump as usual,
				//listen to syncstart channel 
				//add syncstart listener
			case SYNC:
				select{
					case _=<-peerSyncErrorSignal : stage=BOOTSTRAP
					case _=<-peerShutdownSignal : stage=BOOTSTRAP
					case _=<-peerStartupSignal : stage=BOOTSTRAP
					case _=<-peerInSyncSignal : continue
					default : 
				}
			case SHUTTING_DOWN:
				return
		}
	}
}
 
 
 type BoolResponse struct {
    Success bool `json:"success"`
}
var (
	TrueResponseStr = "{\"success\":true}"
	FalseResponseStr = "{\"success\":false}"
)// in high-performance setting, TRS="1", FRS="0" !!!
 
 
var short_timeout = time.Duration(500 * time.Millisecond)
func dialTimeout(network, addr string) (net.Conn, error) {
    return net.DialTimeout(network, addr, short_timeout)
}
var fastTransport http.RoundTripper = &http.Transport{
        Proxy:                 http.ProxyFromEnvironment,
        ResponseHeaderTimeout: short_timeout,
		Dial: dialTimeout,
}
var fastClient = http.Client{
        Transport: fastTransport,
    }
var backup_furl = "http://"+conf["backup"]+":"+strconv.Itoa(backupPort)+"/kv/upsert"
func fastSync(key string, value string, del bool) bool{
	var url = backup_furl+
		"?key="+url.QueryEscape(key)+
		"&value="+url.QueryEscape(value)
	if(del){
		url += "&delete=true"
	}
	//key, value, delete=true
	resp, err := fastClient.Get(url)
	if err != nil {
		peerSyncErrorSignal<- 1
		return false
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		peerSyncErrorSignal<- 1
	}
	if string(body)=="1" ||  string(body)== TrueResponseStr{
		return true
	}
	peerSyncErrorSignal<- 1
	return false
}
 
 //found error: kick to out-of-sync state?
 //c chan int, send error inside
 // go func(){ } maintenance, if found erroneous stuff, then re-sync
 //  including initial state!
 // add more kvman handler!!
 // user regular HTTP client when syncing!
 
 
 //Main program started here
 //methods: insert,delete,update; get (via GET)


type StrResponse struct {
	Success bool `json:"success"`
    Value string `json:"value"`
}

func naive_kvUpsertHandler(w http.ResponseWriter, r *http.Request) {
	key:= r.FormValue("key")
	value:= r.FormValue("value")
	delete:= r.FormValue("delete")
	if(delete == "true"){
		db.Remove(key)
		fmt.Fprintf(w, "%s",TrueResponseStr)
		return
	}
	if db.Set(key,value){
		fmt.Fprintf(w, "%s",TrueResponseStr)
		return
	}
	fmt.Fprintf(w, "%s",FalseResponseStr)
} 
func naive_kvInsertHandler(w http.ResponseWriter, r *http.Request) {
	key:= r.FormValue("key")
	value:= r.FormValue("value")
	if !db.Has(key) && db.Set(key,value){
		fmt.Fprintf(w, "%s",TrueResponseStr)
		return
	}
	fmt.Fprintf(w, "%s",FalseResponseStr)
} 
func naive_kvUpdateHandler(w http.ResponseWriter, r *http.Request) {
	key:= r.FormValue("key")
	value:= r.FormValue("value")
	if db.Has(key) && db.Set(key,value){
		fmt.Fprintf(w, "%s",TrueResponseStr)
		return
	}
	fmt.Fprintf(w, "%s",FalseResponseStr)
} 
func naive_kvDeleteHandler(w http.ResponseWriter, r *http.Request) {
	key:= r.FormValue("key")
	if db.Has(key){
		db.Remove(key)
		fmt.Fprintf(w, "%s",TrueResponseStr)
		return
	}
	fmt.Fprintf(w, "%s",FalseResponseStr)
} 
func naive_kvGetHandler(w http.ResponseWriter, r *http.Request) {
	key:= r.FormValue("key")
	val,ok:= db.Get(key)
	ret:=&StrResponse{
		Success:ok,
		Value:val}
	str,_:=json.Marshal(ret);
	fmt.Fprintf(w, "%s",str)
} 

func primary_kvGetHandler(w http.ResponseWriter, r *http.Request) {
	switch stage {
		case BOOTSTRAP, SYNC:
			naive_kvGetHandler(w, r);
			return
	}
	fmt.Fprintf(w, "%s",FalseResponseStr)
}
func primary_kvInsertHandler(w http.ResponseWriter, r *http.Request) {
	if stage!=SYNC {
		fmt.Fprintf(w, "%s",FalseResponseStr)
		return
	}
	key:= r.FormValue("key")
	value:= r.FormValue("value")
	if !db.Has(key) && db.Set(key,value){
		ret:= fastSync(key,value,false)
		if ret{
			fmt.Fprintf(w, "%s",TrueResponseStr)
			return
		}
		//recover
		db.Remove(key)
	}
	fmt.Fprintf(w, "%s",FalseResponseStr)
}

func primary_kvUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if stage!=SYNC {
		fmt.Fprintf(w, "%s",FalseResponseStr)
		return
	}
	key:= r.FormValue("key")
	value:= r.FormValue("value")
	if db.Has(key){
		recover,_:=db.Get(key)
		if(db.Set(key,value)){
			ret:= fastSync(key,value,false)
			if ret{
					fmt.Fprintf(w, "%s",TrueResponseStr)
					return
			}
		}
		//recover
		db.Set(key,recover)
	}
	fmt.Fprintf(w, "%s",FalseResponseStr)
}
func primary_kvDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if stage!=SYNC {
		fmt.Fprintf(w, "%s",FalseResponseStr)
		return
	}
	key:= r.FormValue("key")
	if db.Has(key){
		recover,_:=db.Get(key)
		db.Remove(key)
		ret:= fastSync(key,"",true)
		if ret{
			ret:=&StrResponse{
				Success:true,
				Value:recover}
			str,_:=json.Marshal(ret);
			fmt.Fprintf(w, "%s",str)
			return
		}
		//recover
		db.Set(key,recover)
	}
	fmt.Fprintf(w, "%s",FalseResponseStr)
}


 
func kvmanCountkeyHandler(w http.ResponseWriter, r *http.Request) {
	tmp := make(map[string]int)
	tmp["result"]=db.Count()
	var str,err=json.Marshal(tmp)
	if err==nil{
		fmt.Fprintf(w, "%s",str)
		return
	}
	fmt.Fprintf(w, "DB marshalling error %s",err)
}
func kvmanDumpHandler(w http.ResponseWriter, r *http.Request) {
	var str,err=db.MarshalJSON();
	if err==nil{
		fmt.Fprintf(w, "%s",str)
		return
	}
	fmt.Fprintf(w, "DB marshalling error %s",err)
}
func kvmanShutdownHandler(w http.ResponseWriter, r *http.Request) {
	stage=SHUTTING_DOWN
	if role==PRIMARY{
		time.Sleep(time.Millisecond*502)
		//allow all existing fastSync to finish
	}
	_,_=http.Get(peerURL+"peershutdown")
	fmt.Fprintf(w, "Hello, %q, DB suicide",
      html.EscapeString(r.URL.Path))
	defer func(){
		time.Sleep(time.Millisecond*1) //sleep epsilon
		os.Exit(0)
	}()
}
func kvmanPeerShutdownHandler(w http.ResponseWriter, r *http.Request) {
	peerShutdownSignal<- 1
}
func kvmanPeerStartupHandler(w http.ResponseWriter, r *http.Request) {
	peerStartupSignal<- 1
	if (role==BACKUP) && ((stage==WARM_START)||(stage==COLD_START)) { //I have no data
		fmt.Fprintf(w, "0")
		return
	}
	fmt.Fprintf(w, "1")
}
func kvmanPeerStartSyncHandler(w http.ResponseWriter, r *http.Request) {
	hash:= r.FormValue("hash")
	str,_:= db.MarshalJSON()
	rhash:= MD5(str)
	if hash==rhash{
		//reply response
		if role==BACKUP{
			peerInSyncSignal <- 1
		}
		fmt.Fprintf(w, "%s",rhash)
		if role==PRIMARY{
			peerInSyncSignal <- 1
		}
		//send in-sync signal, before(i'm back) or after(i'm prim)
		//note: primary should go to SYNC state after secondary
		return
	}
	fmt.Fprintf(w, "0")
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/"{
		time.Sleep(3 * time.Second)
		fmt.Fprintf(w, "Unrecognized Request")
		return
	}
	fmt.Fprintf(w, "Hello, %q, this is a server.",
      html.EscapeString(r.URL.Path))
	fmt.Fprintf(w, "Role:%d, stage:%d, dump:",
      role, stage)
	kvmanDumpHandler(w,r);
}

func main(){
	fmt.Println("Initialized with conf:");
	fmt.Println(conf);
	
	fmt.Print("My role:");
	switch role{
		case PRIMARY:
			fmt.Println("Primary")
		case BACKUP:
			fmt.Println("Backup")
		default :
			fmt.Println("Unknown; please specify role as command line parameter.")
			panic(os.Args)
	}
	fmt.Print("listenPort:")
	fmt.Println(listenPort)
	
	
	//db.Set("_","__");  //dummy key for testing purpose
  
	s := &http.Server{
		Addr: ":"+strconv.Itoa(listenPort),
		Handler: nil,
		ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second,
		MaxHeaderBytes: 1<<20,
	}
	//http.HandleFunc("/kv", kvHandler)
	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/kvman/countkey", kvmanCountkeyHandler)
	http.HandleFunc("/kvman/dump", kvmanDumpHandler)
	http.HandleFunc("/kvman/shutdown", kvmanShutdownHandler)
	http.HandleFunc("/kvman/peershutdown", kvmanPeerShutdownHandler)
	http.HandleFunc("/kvman/peerstartup", kvmanPeerStartupHandler)
	http.HandleFunc("/kvman/peerstartsync", kvmanPeerStartSyncHandler)
	
	if role==BACKUP{// should be if(backup)
		http.HandleFunc("/kv/get", naive_kvGetHandler)
		http.HandleFunc("/kv/insert", naive_kvInsertHandler)
		http.HandleFunc("/kv/update", naive_kvUpdateHandler)
		http.HandleFunc("/kv/delete", naive_kvDeleteHandler)
		http.HandleFunc("/kv/upsert", naive_kvUpsertHandler)
	}else{
		http.HandleFunc("/kv/get", primary_kvGetHandler)
		http.HandleFunc("/kv/insert", primary_kvInsertHandler)
		http.HandleFunc("/kv/update", primary_kvUpdateHandler)
		http.HandleFunc("/kv/delete", primary_kvDeleteHandler)
	}
	
	go housekeeper()
	
	log.Fatal(s.ListenAndServe())  
}
