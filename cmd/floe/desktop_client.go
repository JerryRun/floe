package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"floe/internal/core"
)

type desktopClient struct {
	origin string
	http   *http.Client
}

func newDesktopClient(origin string) *desktopClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &desktopClient{
		origin: strings.TrimRight(origin, "/"),
		http: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

type desktopConflictFile struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Size     *int64    `json:"size"`
	Modified time.Time `json:"modified"`
}

type desktopAPIError struct {
	Code        string               `json:"code"`
	Message     string               `json:"message"`
	Detail      string               `json:"detail"`
	Fingerprint string               `json:"fingerprint"`
	Expected    string               `json:"expected"`
	Received    string               `json:"received"`
	SourcePath  string               `json:"source_path"`
	TargetPath  string               `json:"target_path"`
	Status      int                  `json:"-"`
	Source      *desktopConflictFile `json:"source"`
	Target      *desktopConflictFile `json:"target"`
}

func (e *desktopAPIError) Error() string {
	switch {
	case e.Expected != "" && e.Received != "":
		return fmt.Sprintf("%s（原指纹 %s，新指纹 %s）", e.Message, e.Expected, e.Received)
	case e.Detail != "":
		return e.Message + " · " + e.Detail
	default:
		return e.Message
	}
}

func (c *desktopClient) request(ctx context.Context, method, endpoint string, body, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var content io.Reader
	var contentType string
	switch data := body.(type) {
	case nil:
	case []byte:
		content = bytes.NewReader(data)
		contentType = "application/octet-stream"
	default:
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		content = bytes.NewReader(encoded)
		contentType = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+endpoint, content)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := desktopResponseError(response); err != nil {
		return err
	}
	if result != nil {
		return json.NewDecoder(response.Body).Decode(result)
	}
	_, err = io.Copy(io.Discard, response.Body)
	return err
}

func desktopResponseError(response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	failure := desktopAPIError{Status: response.StatusCode}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&failure); err != nil || failure.Message == "" {
		return fmt.Errorf("Floe API · %s", response.Status)
	}
	return &failure
}

type desktopActivity struct {
	ID       int64     `json:"id"`
	Time     time.Time `json:"time"`
	Level    string    `json:"level"`
	Category string    `json:"category"`
	Message  string    `json:"message"`
	Detail   string    `json:"detail"`
}

func (c *desktopClient) events(ctx context.Context, receiveTasks func([]core.TransferTask), receiveLogs func([]desktopActivity)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+"/api/v1/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	response, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := desktopResponseError(response); err != nil {
		return err
	}
	reader := bufio.NewReader(response.Body)
	var event string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			event = ""
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case event == "tasks" && strings.HasPrefix(line, "data: "):
			var tasks []core.TransferTask
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &tasks); err != nil {
				return err
			}
			receiveTasks(tasks)
		case event == "logs" && receiveLogs != nil && strings.HasPrefix(line, "data: "):
			var logs []desktopActivity
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &logs); err != nil {
				return err
			}
			receiveLogs(logs)
		}
	}
}
