package main

import(
  "net/http"
  "fmt"
  "time"
  "strconv"
  "sort"
  "os"
  "strings"
  "io/ioutil"
  "encoding/json"
  )


var conf = readConf("conf/settings.conf") 
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
var(
 rootURL="http://"+conf["primary"]+":"+conf["port"]+"/"
 kvURL=rootURL+"kv/"
 kvmanURL=rootURL+"kvman/"
)

func naive_HTTP(url string, data_enc string, post bool) (string, error) {
	if post{
		resp, err := http.Post(url,
			"application/x-www-form-urlencoded",
			strings.NewReader(data_enc))
		if err != nil {
			return "",err
		}
			 
		defer resp.Body.Close()
		body, err2 := ioutil.ReadAll(resp.Body)
		if err2 != nil {
			return "",err
		}
		return string(body), nil
    }else{
		if data_enc != "" {
			url+="?"+data_enc
		}
		resp, err := http.Get(url)
		if err != nil {
			return "",err
		}
			 
		defer resp.Body.Close()
		body, err2 := ioutil.ReadAll(resp.Body)
		if err2 != nil {
			return "",err
		}
		return string(body), nil
	}
}
 
func do_insert(key string, value string, c chan time.Duration){
	start := time.Now()
	naive_HTTP(kvURL+"insert","key="+key+"&value="+value,true)
	c<- time.Since(start)
} 
//func do_remove 




type duration_slice []time.Duration
func (a duration_slice) Len() int { return len(a) }
func (a duration_slice) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a duration_slice) Less(i, j int) bool { return a[i] < a[j] }

func main(){
  N:=10000
  
  ret,err := naive_HTTP(kvmanURL+"dump","",false)
  if err!=nil{
    fmt.Println(err)
	os.Exit(-1)
  }
  fmt.Println(ret)
  dummy:="TEST keyvalue long string................"
  for i:=0; i<1000;i++ {
	dummy=dummy+ string(i%26+65)
  }
  
  perf:=make(chan time.Duration, N)
  
  for i:=0; i<N;i++ {
	go do_insert(dummy+strconv.Itoa(i),strconv.Itoa(i)+dummy, perf)
  }
  
  stat:=make(duration_slice, N)  // [N]time.Duration
  //stat:=make([]int64, N)
  for i:=0; i<N;i++ {
	stat[i]= <-perf
  }
  sort.Sort(stat)
  
  for i:=1;i<=9;i++ {
	fmt.Print(strconv.Itoa(i*10)+"% Percentile:")
	fmt.Println(stat[i*N/10])
  }
}
