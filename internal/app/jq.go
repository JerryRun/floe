package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/itchyny/gojq"
)

const (
	maxJQBodyBytes   = 2 << 20
	maxJQQueryLength = 4096
	maxJQResultBytes = 4 << 20
	maxJQResults     = 5000
)

func (s *Server) runJQ(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxJQBodyBytes)
	var request struct {
		Input json.RawMessage `json:"input"`
		Query string          `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "JQ_REQUEST_INVALID", "无法读取 JSON 或过滤表达式", err.Error())
		return
	}
	if len(request.Input) == 0 || !json.Valid(request.Input) {
		writeError(w, http.StatusBadRequest, "JQ_INPUT_INVALID", "输入不是有效的 JSON", "")
		return
	}
	request.Query = strings.TrimSpace(request.Query)
	if len(request.Query) == 0 || len(request.Query) > maxJQQueryLength {
		writeError(w, http.StatusBadRequest, "JQ_QUERY_INVALID", "jq 表达式不能为空且不能超过 4096 字符", "")
		return
	}
	query, err := gojq.Parse(request.Query)
	if err != nil {
		writeError(w, http.StatusBadRequest, "JQ_QUERY_INVALID", "jq 表达式有误", err.Error())
		return
	}
	code, err := gojq.Compile(query, gojq.WithEnvironLoader(func() []string { return nil }))
	if err != nil {
		writeError(w, http.StatusBadRequest, "JQ_QUERY_INVALID", "jq 表达式无法编译", err.Error())
		return
	}
	decoder := json.NewDecoder(strings.NewReader(string(request.Input)))
	decoder.UseNumber()
	var input any
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "JQ_INPUT_INVALID", "无法解析输入的 JSON", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	iterator := code.RunWithContext(ctx, input)
	results := make([]string, 0)
	resultBytes := 0
	for {
		if err := ctx.Err(); err != nil {
			writeError(w, http.StatusRequestTimeout, "JQ_TIMEOUT", "jq 执行超时，请简化表达式或输入", "")
			return
		}
		value, ok := iterator.Next()
		if !ok {
			break
		}
		if err, ok := value.(error); ok {
			writeError(w, http.StatusBadRequest, "JQ_EVALUATION_FAILED", "jq 表达式执行失败", err.Error())
			return
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			writeError(w, http.StatusBadRequest, "JQ_RESULT_INVALID", "jq 的结果无法转成 JSON", err.Error())
			return
		}
		resultBytes += len(encoded)
		if resultBytes > maxJQResultBytes || len(results) >= maxJQResults {
			writeError(w, http.StatusRequestEntityTooLarge, "JQ_RESULT_TOO_LARGE", fmt.Sprintf("jq 结果超过限制（最多 %d 条、共 4 MiB）", maxJQResults), "")
			return
		}
		results = append(results, string(encoded))
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
