package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/cheggaaa/pb"
)

//Worker is struct to store current working url and other things
type Worker struct {
	URL       string
	File      *os.File
	Count     int64
	SyncWG    sync.WaitGroup
	TotalSize int64
	Progress
}

//Progress is a struct to store pb aka progressBars from 'github.com/cheggaaa/pb'
type Progress struct {
	Pool *pb.Pool
	Bars []*pb.ProgressBar
}

func (w *Worker) writeRange(partNum int64, start int64, end int64) {
	var written int64
	body, size, err := w.getRangeBody(start, end)
	if err != nil {
		log.Fatalf("Part %d request error :%s\n", partNum, err.Error())
	}
	defer body.Close()
	defer w.Bars[partNum].Finish()
	defer w.SyncWG.Done()

	w.Bars[partNum].Total = size

	percentFlag := map[int64]bool{}

	buf := make([]byte, 4*1024)
	for {
		nr, er := body.Read(buf)
		if nr > 0 {
			nw, err := w.File.WriteAt(buf[0:nr], start)
			if err != nil {
				log.Fatalf("Part %d occured error %s\n", partNum, err.Error())
			}
			if nr != nw {
				log.Fatalf("Part %d occured error of short writing \n", partNum)
			}
			start += int64(nw)
			if nw > 0 {
				written += int64(nw)
			}
			w.Bars[int(partNum)].Set64(written)

			p := int64(float32(written) / float32(size) * 100)
			_, flagged := percentFlag[p]
			if !flagged {
				percentFlag[p] = true
				w.Bars[int(partNum)].Prefix(fmt.Sprintf("Part %d %d", partNum, p))
			}
		}
		if er != nil {
			if er.Error() == "EOF" {
				if size == written {
					log.Println("Downloaded successfully")
				} else {
					handleError(fmt.Errorf("Part %d unfinished", partNum))
				}
				break
			}
			handleError(fmt.Errorf("Part %d occured error %s", partNum, er.Error()))
		}
	}
}
func (w *Worker) getRangeBody(start int64, end int64) (io.ReadCloser, int64, error) {
	var client http.Client
	req, err := http.NewRequest("GET", w.URL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	size, err := strconv.ParseInt(res.Header["Content-Length"][0], 10, 64)
	return res.Body, size, err
}
func main() {
	var t = flag.Bool("t", false, "boolean to include datetime in file name")
	var workerCount = flag.Int64("c", int64(runtime.NumCPU()), "Number of connections")
	inputURL := flag.String("url", "", "Input url to download")
	flag.Parse()
	var downloadURL = *inputURL
	if downloadURL == "" {
		log.Println(fmt.Errorf("Download URL is not Provided. Run with `-help` attribute for available options\n[Exiting...................................................]"))
		os.Exit(1)
	}
	log.Println("URL :", downloadURL)
	fileSize, supported, err := getSizeAndCheckRangeSupport(downloadURL)
	if !supported {
		*workerCount = 1
	}
	handleError(err)
	log.Println("File Size : ", fileSize)
	var filePath string
	currentUser, _ := user.Current()
	if *t {
		filePath = currentUser.HomeDir + string(filepath.Separator) + "Downloads" + string(filepath.Separator) + strconv.FormatInt(time.Now().UnixNano(), 16) + "_" + getFileName(downloadURL)
	} else {
		filePath = currentUser.HomeDir + string(filepath.Separator) + "Downloads" + string(filepath.Separator) + getFileName(downloadURL)
	}
	//log.Println("Local path : ", filePath)
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, os.ModeAppend)
	handleError(err)
	defer f.Close()

	var worker = Worker{
		URL:       downloadURL,
		File:      f,
		Count:     *workerCount,
		TotalSize: fileSize,
	}
	var start, end int64
	var partialSize = int64(fileSize / *workerCount)
	now := time.Now().UTC()
	for num := int64(0); num < worker.Count; num++ {
		bar := pb.New(0).Prefix(fmt.Sprintf("Part %d ", num))
		bar.ShowSpeed = true
		bar.SetMaxWidth(100)
		bar.SetUnits(pb.U_BYTES_DEC)
		bar.SetRefreshRate(time.Second)
		bar.ShowPercent = true
		worker.Progress.Bars = append(worker.Progress.Bars, bar)
		if num == worker.Count {
			end = fileSize
		} else {
			end = start + partialSize
		}

		worker.SyncWG.Add(1)
		go worker.writeRange(num, start, end-1)
		start = end
	}
	worker.Progress.Pool, err = pb.StartPool(worker.Progress.Bars...)
	handleError(err)
	worker.SyncWG.Wait()
	worker.Progress.Pool.Stop()
	log.Println("Elapsed time :", time.Since(now))
	log.Println("DONE! Saved to " + filePath)
	blockForWindows()
}

func getFileName(downloadURL string) string {
	urlStruct, err := url.Parse(downloadURL)
	handleError(err)
	return filepath.Base(urlStruct.Path)
}
func getSizeAndCheckRangeSupport(url string) (size int64, supported bool, err error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	res, err := client.Do(req)
	if err != nil {
		return
	}
	//log.Println("Response Header :", res.Header)
	header := res.Header
	size, err = strconv.ParseInt(header["Content-Length"][0], 10, 64)
	acceptRanges, supported := header["Accept-Ranges"]
	if !supported {
		return size, true, err
	} else if supported && acceptRanges[0] != "bytes" {
		return 0, false, errors.New("Support `Accept-Ranges` but value is not `bytes`")
	}
	return
}

func handleError(err error) {
	if err != nil {
		log.Println("Error :", err)
		blockForWindows()
		os.Exit(1)
	}
}
func blockForWindows() {
	if runtime.GOOS == "windows" {
		for {
			log.Println("[Press CTRL+C to EXIT......]")
			time.Sleep(10 * time.Second)
		}
	}
}
