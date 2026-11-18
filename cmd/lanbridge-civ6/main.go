package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"
)

var networkIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{16}$`)

type response struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error"`
	Data  json.RawMessage `json:"data"`
}

func request(client *http.Client, method, address string, body any) (json.RawMessage, error) {
	var input io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, address, input)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接本机 LANBridge 后台：%w", err)
	}
	defer resp.Body.Close()
	var result response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("后台响应无效：%w", err)
	}
	if resp.StatusCode >= 300 || !result.OK {
		if result.Error == "" {
			result.Error = resp.Status
		}
		return nil, errors.New(result.Error)
	}
	return result.Data, nil
}

func run(args []string, output io.Writer, client *http.Client, base string) error {
	if len(args) == 0 {
		return errors.New("用法：lanbridge-civ6 status | connect <16位网络ID>")
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return errors.New("用法：lanbridge-civ6 status")
		}
		data, err := request(client, http.MethodGet, base+"/api/zt/health", nil)
		if err != nil {
			return err
		}
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, data, "", "  "); err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, formatted.String())
		return err
	case "connect":
		if len(args) != 2 || !networkIDPattern.MatchString(args[1]) {
			return errors.New("用法：lanbridge-civ6 connect <16位十六进制网络ID>")
		}
		_, err := request(client, http.MethodPost, base+"/api/zt/join", map[string]string{"network_id": args[1]})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, "网络已提交加入；请使用 status 确认 ready 状态。")
		return err
	default:
		return errors.New("用法：lanbridge-civ6 status | connect <16位网络ID>")
	}
}

func main() {
	client := &http.Client{Timeout: 60 * time.Second}
	if err := run(os.Args[1:], os.Stdout, client, "http://127.0.0.1:8899"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
