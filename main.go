package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	bufSize = 1024 * 1024
)

var (
	realURL string
)

type downmsg struct {
	url, cookie, ref string
	st, ed           int64
}
type lockFile struct {
	lock sync.Locker
	f    *os.File
}

func parsecl() (n int, c, url, path, ref string) {
	cmdl := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	cmdl.Usage = func() {
		fmt.Fprintf(os.Stderr, "\n多线程下载器 by yuansu\n\nUsage: %s [option] URL\n", os.Args[0])
		cmdl.PrintDefaults()
	}
	cmdl.StringVar(&path, "o", "", "文件输出位置")
	cmdl.IntVar(&n, "n", 1, "同时下载的线程数量[1-50]")
	cmdl.StringVar(&c, "c", "", "要传递的Cookie")
	cmdl.StringVar(&ref, "r", "", "引用页")
	cmdl.Parse(os.Args[1:])
	if n < 1 {
		n = 1
	}
	if n > 50 {
		n = 50
	}

	if cmdl.NArg() == 0 {
		cmdl.Usage()
		os.Exit(1)
	} else {
		url = cmdl.Arg(0)
	}
	return

}

func main() {
	//获得命令号参数
	threadNum, c, url, filename, ref := parsecl()

	//获取文件大小
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			realURL = req.URL.String()
			fmt.Printf("\n重定向至：%s\n", realURL)
			return nil
		}}
	req, err := http.NewRequest("HEAD", url, nil)
	if c != "" {
		req.Header.Add("Cookie", c)
	}
	if ref != "" {
		req.Header.Add("Referer", ref)
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal("连接失败！" + err.Error())
	}
	var fileLength int64
	if t, ok := resp.Header["Content-Length"]; ok {
		fileLength, _ = strconv.ParseInt(t[0], 10, 64)
	} else {
		log.Fatal("无法获取文件长度，不能多线程下载")
	}

	//不指定文件名时，从网址中获取文件名，优先使用跳转后的网址
	if filename == "" {
		if realURL != "" {
			place := strings.LastIndex(realURL, "/")
			filename = realURL[place+1:]
		} else {
			place := strings.LastIndex(url, "/")
			filename = url[place+1:]
		}
	}

	//截断过长文件名
	if l := len(filename); l > 100 {
		filename = filename[l-100:]
	}

	reporter := make(chan int)
	isend := make(chan bool)
	client2 := &http.Client{}
	f, err := os.Create(filename)
	defer f.Close()
	if err != nil {
		log.Fatal("无法创建文件:", filename)
	}
	mutex := sync.Mutex{}
	dataslice := splitrange(fileLength, int64(threadNum))
	fmt.Printf("\n开始下载: %s [%s]\n", filename, fmtSize(fileLength))
	var t1, t2 int64
	t1 = time.Now().Unix()

	go progressor(fileLength, reporter, isend)
	for i := 0; i < threadNum; i++ {
		go downloader(client2, downmsg{url: url, cookie: c, ref: ref, st: dataslice[i], ed: dataslice[i+1] - 1}, lockFile{f: f, lock: &mutex}, reporter)
	}
	<-isend
	t2 = time.Now().Unix()
	speed := fileLength / (t2 - t1)
	fmt.Printf("\n下载完成！平均速度：%s/s\n", fmtSize(speed))
}

//分配各线程下载范围
func splitrange(size, n int64) []int64 {
	step := size / n
	arr := make([]int64, n+1)
	arr[0] = 0
	var i int64
	for i = 1; i <= n; i++ {
		arr[i] = step * i
	}
	arr[n] = size
	return arr
}

//单个下载线程
func downloader(client *http.Client, msg downmsg, f lockFile, reporter chan int) {
	if msg.st >= msg.ed {
		return
	}
	req, _ := http.NewRequest("GET", msg.url, nil)
	if msg.ref != "" {
		req.Header.Add("Referer", msg.ref)
	}
	if msg.cookie != "" {
		req.Header.Add("Cookie", msg.cookie)
	}
	req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", msg.st, msg.ed))
	buf := make([]byte, bufSize)

	resp, err := client.Do(req)
	if err != nil {
		log.Fatal("分线程下载失败")
	}
	var n int
	process := int64(msg.st)

	for {
		n, err = resp.Body.Read(buf)
		if n == 0 && err != nil {
			break
		}
		reporter <- n
		f.lock.Lock()
		f.f.WriteAt(buf[:n], process)
		process += int64(n)
		f.lock.Unlock()
	}

}

//显示进度
func progressor(max int64, p chan int, isend chan bool) {
	var sum, lastsum int64
	var t1, t2 int64
	var speed int64
	var speedstr string
	t1 = time.Now().UnixNano()
	for i := range p {
		sum += int64(i)
		t2 = time.Now().UnixNano()
		if du := t2 - t1; du > 3e8 {
			du /= 1e8 //0.1s
			speed = (sum - lastsum) * 10 / du
			speedstr = fmtSize(speed) + "/s"
			t1 = t2
			lastsum = sum
		}
		fmt.Printf("进度：[%%% 3.1f] 下载速度：[% 10s]\r", float32(sum)/float32(max)*100, speedstr)
		if sum >= max {
			break
		}

	}
	isend <- true

}

func fmtSize(size int64) (result string) {
	if size < 1024 {
		result = fmt.Sprintf("%dB", size)
	} else if size < 1024*1024 {
		result = fmt.Sprintf("%.1fKB", float32(size)/1024)
	} else if size < 1024*1024*1024 {
		result = fmt.Sprintf("%.1fMB", float32(size)/1024/1024)
	} else {
		result = fmt.Sprintf("%.1fGB", float32(size)/1024/1024/1024)
	}
	return
}
