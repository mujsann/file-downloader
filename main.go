package main

// download file by chunking
// download based on cusotm number of parts of the file in parallel (using go routines)
// concatenate all parts into a go-routine

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ResHeaders struct {
	ContentLegnth      string
	ContentDisposition string
	ContentType        string
}

const NUM_PARTS = 4
const MAX_RETRIES = 3

func main() {
	// collect web url and destination from cli
	// define flags
	fURL := flag.String("url", "", "File's download url")
	dest := flag.String("dest", "", "File's destination path on local computer")

	flag.Parse()

	err := validateUrl(*fURL)
	if err != nil {
		log.Fatal("File's download url is not valid")
	}

	// background context
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()

	err = downloadFile(ctx, *fURL, *dest)
	if err != nil {
		fmt.Printf("\n Download failed: %v", err)
	}

}

// ensure that it's a valid url
func validateUrl(fURL string) error {
	_, err := url.ParseRequestURI(fURL)
	if err != nil {
		return err
	}

	return nil
}

func getFileHeaders(url string) (*ResHeaders, error) {
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HEAD request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to perform HEAD request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned non-200 status: %s", resp.Status)
	}

	cl := resp.Header.Get("Content-Length")
	if cl == "" {
		return nil, fmt.Errorf("Content-Length header is missing")
	}

	cd := resp.Header.Get("Content-Dispostion")
	if cd == "" {
		log.Printf("Content-Disposition header is missing")
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		log.Printf("Content-Type header is missing")
	}

	return &ResHeaders{
		ContentLegnth:      cl,
		ContentDisposition: cd,
		ContentType:        ct,
	}, nil

}

func detectFileType(contentType string) string {

	// clean the Content-Type in case of e.g Content-Type: text/html; charset=UTF-8
	contentType = strings.Split(contentType, ";")[0]

	// verify mime type
	extensions, err := mime.ExtensionsByType(contentType)
	if err != nil {
		log.Printf("error getting extensions for Content-Type '%s': %v", contentType, err)
		return ""
	}

	if len(extensions) > 0 {
		extension := extensions[0]
		return extension
	}

	log.Printf("failed to find extensions for Content-Type '%s'", contentType)
	return ""

}

// get file name form content-disposition, else use url
func getFileName(contentDisposition, url string) string {
	if contentDisposition != "" {
		if _, params, err := mime.ParseMediaType(contentDisposition); err == nil {
			if filename, ok := params["filename"]; ok {
				return filename
			}
		}
	}

	// Fallback to URL parsing
	if filename := path.Base(url); filename != "" {
		return filename
	}

	// if base url is empty return a random string
	return "random" + strconv.Itoa(int(rand.Int64()))

}

func getFileSize(url string) (int64, error) {
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create HEAD request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to perform HEAD request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("server returned non-200 status: %s", resp.Status)
	}

	cl := resp.Header.Get("Content-Length")
	if cl == "" {
		return 0, fmt.Errorf("Content-Length header is missing")
	}

	size, err := strconv.ParseInt(cl, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid Content-Length value: %v", err)
	}

	return size, nil
}

func downloadPart(ctx context.Context, url string, start, end int64, partNum int, wg *sync.WaitGroup, tempFiles chan<- string, errChan chan<- error) {

	defer wg.Done()

	var resp *http.Response
	var err error

	for attempt := 1; attempt <= MAX_RETRIES; attempt++ {

		// craete an http request with Range header
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			errChan <- fmt.Errorf("part %d: failed to create GET request: %v", partNum, err)
			return

		}

		rangeHeader := fmt.Sprintf("bytes=%d-%d", start, end)
		req.Header.Set("Range", rangeHeader)

		// send the created request
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			if attempt < MAX_RETRIES {
				fmt.Printf("part %d: attempt %d failed: %v. Retrying...\n", partNum, attempt, err)
				time.Sleep(time.Second * time.Duration(attempt))
				continue
			}

			errChan <- fmt.Errorf("part %d: failed to perform GET request after %d attempts: %v", partNum, attempt, err)
			return
		}

		// Check for Partial Content status
		if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if attempt < MAX_RETRIES {
				fmt.Printf("part %d: server returned status %s. Retrying...\n", partNum, resp.Status)
				time.Sleep(time.Second * time.Duration(attempt))
				continue
			}
			errChan <- fmt.Errorf("part %d: server returned status: %s", partNum, resp.Status)
			return
		}

		// if successful response, don't retry
		break

	}

	defer resp.Body.Close()

	// create a temporary file for this part
	tempFileName := fmt.Sprintf("part-%d.tmp", partNum)
	tempFile, err := os.Create(tempFileName)
	if err != nil {
		errChan <- fmt.Errorf("part %d: failed to create temp file: %v", partNum, err)
		return
	}

	defer tempFile.Close()

	// write the response body to the temp file
	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		errChan <- fmt.Errorf("part %d: failed to write to temp file: %v", partNum, err)
		return
	}

	// send the temp file name to the channel
	tempFiles <- tempFileName

}

func downloadFile(ctx context.Context, url, filepath string) error {

	// get neccessary file headers
	fHeaders, err := getFileHeaders(url)
	if err != nil {
		return fmt.Errorf("failed to get file headers: %v", err)
	}

	// get total file size
	totalSize, err := getFileSize(url)
	if err != nil {
		return fmt.Errorf("failed to get file size: %v", err)
	}
	fmt.Printf("Total file size: %d bytes\n", totalSize)

	// calculate byte range for each part
	var ranges [NUM_PARTS][2]int64
	partSize := totalSize / NUM_PARTS

	for i := 0; i < NUM_PARTS; i++ {
		start := int64(i) * partSize
		var end int64
		if i == NUM_PARTS-1 {
			// last part goes till the end
			end = totalSize - 1
		} else {
			end = start + partSize - 1
		}

		// create a 2D array of start -> end
		ranges[i][0] = start
		ranges[i][1] = end
	}
	fmt.Printf("Total ranges:%v ", ranges)

	// download each part concurrently using the 2D array
	var wg sync.WaitGroup
	tempFiles := make(chan string, NUM_PARTS)
	errChan := make(chan error, NUM_PARTS)

	for i := 0; i < NUM_PARTS; i++ {
		wg.Add(1)
		go downloadPart(ctx, url, ranges[i][0], ranges[i][1], i+1, &wg, tempFiles, errChan)
	}

	// close all goroutines
	wg.Wait()
	close(tempFiles)
	close(errChan)

	// return the first error, if any errors
	if len(errChan) > 0 {
		for e := range errChan {
			return e
		}
	}

	// create file name from response or url
	filepath += "/" + getFileName(fHeaders.ContentDisposition, url) + detectFileType(fHeaders.ContentType)

	// concat all parts into a file in the destination
	outFile, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	defer outFile.Close()

	for i := 1; i <= NUM_PARTS; i++ {
		tempFileName := fmt.Sprintf("part-%d.tmp", i)
		tempFile, err := os.Open(tempFileName)
		if err != nil {
			return fmt.Errorf("failed to open temp file %s: %v", tempFileName, err)

		}

		_, err = io.Copy(outFile, tempFile)
		tempFile.Close()
		if err != nil {
			return fmt.Errorf("failed to write temp file %s to output: %v", tempFileName, err)
		}

		// clean up
		os.Remove(tempFileName)
	}

	return nil

}
