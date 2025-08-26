package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var directory = flag.String("directory", "", "Directory where the files are stored")

type RequestHeader struct {
	Host           string
	UserAgent      string
	Accept         string
	ContentType    string
	ContentLength  string
	Connection     string
	AcceptEncoding []string
}

type RequestBody struct {
	Body []byte
}

func newRequestHeader() RequestHeader {
	return RequestHeader{}
}

func newRequestBody() RequestBody {
	return RequestBody{}
}

func statusLine200Response() string {
	return "HTTP/1.1 200 OK\r\n"
}

func statusLine201Response() string {
	return "HTTP/1.1 201 Created\r\n\r\n"
}

func (reqHeader RequestHeader) headersResponse(contType string, contLength int, contEncoding string) string {
	if contType == "" {
		if reqHeader.Connection == "close" {
			return "Connection: close\r\n\r\n"
		}
		return "\r\n"
	} else if contEncoding != "" {
		return fmt.Sprintf("Content-Type: %s\r\nContent-Length: %d\r\nContent-Encoding: %s\r\n\r\n", contType, contLength, contEncoding)
	} else if reqHeader.Connection == "close" {
		return fmt.Sprintf("Content-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", contType, contLength)
	}
	return fmt.Sprintf("Content-Type: %s\r\nContent-Length: %d\r\n\r\n", contType, contLength)
}

func gzipBody(body string) ([]byte, error) {
	data := []byte(body)
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	// defer gzipWriter.Close()

	_, writeErr := gzipWriter.Write(data)
	if writeErr != nil {
		fmt.Println("Error: failed to write to gzip writer:", writeErr)
		return []byte(""), fmt.Errorf("error: failed to write to gzip writer: %s", writeErr)
	}
	closeErr := gzipWriter.Close()
	if closeErr != nil {
		fmt.Println("Error: failed to close gzip writer:", closeErr)
		return []byte(""), fmt.Errorf("error: failed to close gzip writer: %s", closeErr)
	}
	compressedData := buf.Bytes()
	return compressedData, nil
}

func getEndpointResponse(endpointArr []string, reqHead RequestHeader, reqBody RequestBody) (string, error) {
	// endpoint[0] is just a space
	endpoint := endpointArr[1]
	switch endpoint {
	case "echo":
		str := endpointArr[2]
		var resp string
		isEncoded := false
		for _, encoding := range reqHead.AcceptEncoding {
			if isValidEncoding(encoding) {
				compressErr := compressData(encoding, str, &reqBody) // Will store the compressed data in reqBody
				if compressErr != nil {
					fmt.Println("Error: Failure to compress data", compressErr)
					return "", compressErr
				}
				resp = fmt.Sprintf("%s%s%s", statusLine200Response(), reqHead.headersResponse("text/plain", len(reqBody.Body), encoding), reqBody.Body)
				isEncoded = true
			}
		}
		// fmt.Println("reqHead.AcceptEncoding
		if !isEncoded {
			// headerResponse = headersResponse("application/octet-stream", count, "")
			resp = fmt.Sprintf("%s%s%s", statusLine200Response(), reqHead.headersResponse("text/plain", len(str), ""), str)
		}

		// resp := fmt.Sprintf("%s%s%s", statusLine200Response(), headersResponse("text/plain", len(str), ""), str)
		return resp, nil
	case "user-agent":
		str := strings.TrimRight(reqHead.UserAgent, "\r\n")
		resp := fmt.Sprintf("%s%s%s", statusLine200Response(), reqHead.headersResponse("text/plain", len(str), ""), str)
		return resp, nil
	case "files":
		fileName := endpointArr[2]
		path := fmt.Sprintf("%s%s", *directory, fileName)
		fmt.Println("path: ", path)
		file, openFileErr := os.Open(path)
		defer file.Close()
		if openFileErr != nil {
			return "", fmt.Errorf("HTTP/1.1 404 Not Found\r\n\r\n")
		}
		fileData := make([]byte, 4096)
		count, readErr := file.Read(fileData)
		if readErr != nil {
			return "", fmt.Errorf("HTTP/1.1 500 Internal Server Error\r\n\r\n")
		}
		var headerResponse string
		isEncoded := false
		for _, reqHeadVal := range reqHead.AcceptEncoding {
			if isValidEncoding(reqHeadVal) {
				headerResponse = reqHead.headersResponse("application/octet-stream", count, reqHeadVal)
				isEncoded = true
			}
		}
		if !isEncoded {
			headerResponse = reqHead.headersResponse("application/octet-stream", count, "")
		}
		resp := fmt.Sprintf("%s%s%s", statusLine200Response(), headerResponse, fileData[:count])
		return resp, nil
	}
	return "", fmt.Errorf("HTTP/1.1 404 Not Found\r\n\r\n")
}

func postEndpointResponse(endpointArr []string, reqBody RequestBody) (string, error) {
	// endpointArr[0] is a space
	endpoint := endpointArr[1]
	switch endpoint {
	case "files":
		fileName := endpointArr[2]
		path := filepath.Join(*directory, fileName)
		// fmt.Println("file in PER:", path)
		// fmt.Println("directory in PER:", *directory)
		mkdirErr := os.MkdirAll(filepath.Dir(path), 0755)
		if mkdirErr != nil {
			fmt.Println("Error: Could not create directory", mkdirErr)
			return "", fmt.Errorf("HTTP/1.1 500 Internal Server Error\r\n\r\n")
		}
		file, createFileErr := os.Create(path)
		if createFileErr != nil {
			fmt.Println("Error: Could not create file\n", createFileErr)
			return "", fmt.Errorf("HTTP/1.1 500 Internal Server Error\r\n\r\n")
		}
		defer file.Close()
		_, writeErr := file.Write(reqBody.Body)
		if writeErr != nil {
			fmt.Println("Error: Failed to write to the given file", writeErr)
			return "", fmt.Errorf("HTTP/1.1 500 Internal Server Error\r\n\r\n")
		}
		resp := statusLine201Response()
		// fmt.Println("resp in PER", resp)
		return resp, nil
	}
	return "", fmt.Errorf("HTTP/1.1 404 Not Found\r\n\r\n")
}

func readRequestHeaderLine(reader *bufio.Reader) (string, error) {
	// Headers: Reading and Processing
	headerLine, headReadErr := reader.ReadString('\n')
	if headReadErr != nil {
		fmt.Println("Error reading header line:", headReadErr)
	}
	return headerLine, headReadErr
}

func readAndStoreRequestHeader(reader *bufio.Reader, reqHeader *RequestHeader) error {
	headerLine, headReadErr := readRequestHeaderLine(reader)
	if headReadErr != nil {
		return headReadErr
	}
	// The end of the request header is marked by a line that just contains "\r\n"
	for headerLine != "\r\n" {
		// headerLineWordsArr := strings.Split(headerLine, " ")
		storeRequestHeaderInfo(headerLine, reqHeader)
		headerLine, headReadErr = readRequestHeaderLine(reader)
		if headReadErr != nil {
			return headReadErr
		}
	}
	return nil
}

func storeRequestHeaderInfo(headerLine string, reqHeader *RequestHeader) {
	// fmt.Println("header line words: ", headerLineWordsArr)
	trimmedHeaderLine := strings.TrimSpace(headerLine)
	headerLineWordsArr := strings.SplitN(trimmedHeaderLine, ":", 2)
	if len(headerLineWordsArr) < 2 {
		fmt.Println("malformed header", headerLine)
		return
	}
	header := strings.TrimSpace(headerLineWordsArr[0])
	reqHeaderVal := strings.TrimSpace(headerLineWordsArr[1])
	fmt.Println("header:", header)
	// fmt.Println("reqHeaderVal:", reqHeaderVal)
	switch header {
	case "Host":
		reqHeader.Host = reqHeaderVal
	case "User-Agent":
		reqHeader.UserAgent = reqHeaderVal
	case "Accept":
		reqHeader.Accept = reqHeaderVal
	case "Content-Type":
		reqHeader.ContentType = reqHeaderVal
	case "Content-Length":
		reqHeader.ContentLength = reqHeaderVal
	case "Connection":
		reqHeader.Connection = reqHeaderVal
	case "Accept-Encoding":
		compArr := strings.Split(reqHeaderVal, ",")
		for _, val := range compArr {
			compType := strings.TrimSpace(val)
			if isValidEncoding(compType) {
				reqHeader.AcceptEncoding = append(reqHeader.AcceptEncoding, compType)
			}
		}
	}
}

func isValidEncoding(possibleEncoding string) bool {
	switch possibleEncoding {
	case "gzip":
		return true
	default:
		return false
	}
}

func compressData(encodingType string, requestBody string, reqBody *RequestBody) error {
	switch encodingType {
	case "gzip":
		fmt.Println("compressing data gzip")
		compressedData, gzipErr := gzipBody(requestBody)
		if gzipErr != nil {
			return gzipErr
		}
		reqBody.Body = compressedData
	}
	return nil
}

func readRequestBody(reader *bufio.Reader, contentLength int) (string, error) {
	body := make([]byte, contentLength)
	n, err := io.ReadFull(reader, body)
	if err != nil {
		return "", err
	}
	if n != contentLength {
		return "", fmt.Errorf("failed to read full request body")
	}
	return string(body), nil
}

func readAndStoreRequestBody(reader *bufio.Reader, reqHeader RequestHeader, reqBody *RequestBody) error {
	// contentLenString := strings.TrimRight(reqHeader.ContentLength, "\r\n")
	contentLenString := reqHeader.ContentLength
	fmt.Println("contentLenString:", contentLenString)
	if contentLenString != "" {
		contentLen, convErr := strconv.Atoi(contentLenString)
		if convErr != nil {
			return fmt.Errorf("Error: invalid Content-Length: %v", convErr)
		}
		body, readRequestBodyErr := readRequestBody(reader, contentLen)
		if readRequestBodyErr != nil {
			return readRequestBodyErr
		}

		isCompressed := false
		for _, encoding := range reqHeader.AcceptEncoding {
			if isValidEncoding(encoding) {
				fmt.Println("encoding:", encoding)
				isCompressed = true
				compressErr := compressData(encoding, body, reqBody)
				if compressErr != nil {
					return compressErr
				}

			}
		}
		if !isCompressed {
			reqBody.Body = []byte(body)
		}

	}
	return nil
}

func readRequest(reader *bufio.Reader) (string, error) {
	reqLine, reqReadErr := reader.ReadString('\n')
	if reqReadErr != nil {
		fmt.Println("Error reading request line:", reqReadErr)
	}
	return reqLine, reqReadErr
}

func respond(conn net.Conn, reqLineWordsArr []string, reqHeader RequestHeader, reqBody RequestBody) {
	requestMethod := reqLineWordsArr[0] // i.e. GET, POST
	// fmt.Println("requestMethod:", requestMethod)
	path := reqLineWordsArr[1]
	endpointArr := strings.Split(path, "/") // first entry in array should be a space

	if path == "/" {
		response := fmt.Sprintf("%s%s", statusLine200Response(), reqHeader.headersResponse("", 0, ""))
		conn.Write([]byte(response))
	} else if len(endpointArr) > 0 {
		if requestMethod == "GET" {
			response, responseErr := getEndpointResponse(endpointArr, reqHeader, reqBody)
			if responseErr != nil {
				// responseErr = "HTTP/1.1 404 Not Found\r\n\r\n"
				conn.Write([]byte(responseErr.Error()))
				return
			}
			conn.Write([]byte(response))
		} else if requestMethod == "POST" {
			response, responseErr := postEndpointResponse(endpointArr, reqBody)
			if responseErr != nil {
				conn.Write([]byte(responseErr.Error()))
				return
			}
			conn.Write([]byte(response))
		}
	}
}

func handleConn(conn net.Conn) {
	defer conn.Close()
	// Request line: Reading and Processing
	reader := bufio.NewReader(conn)
	for {
		reqLine, reqReadErr := readRequest(reader)
		if reqReadErr != nil {
			break
		}
		fmt.Println("reqLine: ", reqLine)

		// Check for 400 Bad Request
		reqLineWordsArr := strings.Split(reqLine, " ")
		if len(reqLineWordsArr) < 2 {
			conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			break
		}
		// Reading and storing the request header information into a RequestHeader struct
		reqHeader := newRequestHeader()
		rAndSError := readAndStoreRequestHeader(reader, &reqHeader)
		if rAndSError != nil {
			fmt.Println("Error: Failed to read or store reponse header: ", rAndSError)
			break
		}
		reqBody := newRequestBody()
		rAndSBodyError := readAndStoreRequestBody(reader, reqHeader, &reqBody)
		fmt.Println("reqBody.Body", string(reqBody.Body))
		if rAndSBodyError != nil && rAndSBodyError != io.EOF {
			fmt.Println("Error: Failed to read or store reponse body: ", rAndSBodyError)
			break
		}
		respond(conn, reqLineWordsArr, reqHeader, reqBody)

		if reqHeader.Connection == "close" {
			break
		}
	}

}

func acceptConnections(listener net.Listener, connChan chan net.Conn) {
	for {
		conn, connErr := listener.Accept()
		if connErr != nil {
			fmt.Println("Error accepting connection: ", connErr.Error())
			os.Exit(1)
		}
		connChan <- conn
	}

}

func main() {
	flag.Parse()

	l, listenErr := net.Listen("tcp", "0.0.0.0:4221")
	if listenErr != nil {
		fmt.Println("Failed to bind to port 4221")
		os.Exit(1)
	}
	connChan := make(chan net.Conn)
	go acceptConnections(l, connChan)
	for {
		conns := <-connChan
		go handleConn(conns)
	}
}
