package easygo

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
)

// Forward 把 TP 编码的 HTTP/1.1 请求文本转发给本车 API，返回 HTTP/1.1 响应文本。
// 必须走 net/http 标准栈：手写 half-close（CloseWrite + io.ReadAll）会被车端
// HTTP 服务直接丢弃不响应（实测返回空），表现为 CI 上 readiness 轮询永远
// "vehicle returned an empty response"。
func (v Vehicle) Forward(ctx context.Context, request []byte) ([]byte, error) {
	if len(bytes.TrimSpace(request)) == 0 {
		return nil, fmt.Errorf("request is empty")
	}
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(request)))
	if err != nil {
		return nil, fmt.Errorf("parse vehicle request: %w", err)
	}
	req.RequestURI = ""
	req.URL.Scheme = "http"
	req.URL.Host = fmt.Sprintf("127.0.0.1:%d", v.runtime().apiPort)
	req.Close = true
	transport := &http.Transport{}
	if v.Dial != nil {
		transport.DialContext = v.Dial
	}
	resp, err := (&http.Client{Transport: transport}).Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "HTTP/1.1 %d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode))
	// Content-Length 以解码后的 body 为准（车端可能 chunked 或重复写头）。
	resp.Header.Del("Content-Length")
	if err := resp.Header.Write(&buf); err != nil {
		return nil, err
	}
	fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(body))
	buf.Write(body)
	return buf.Bytes(), nil
}
