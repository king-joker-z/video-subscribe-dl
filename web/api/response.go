package api

import (
	"encoding/json"
	"net/http"
)

// 统一响应格式
type Response struct {
	Code    int         `json:"code"`
	Data    interface{} `json:"data"`
	Message string      `json:"message"`
}

// PaginatedData 分页数据
type PaginatedData struct {
	Items    interface{} `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

// 错误码体系
const (
	CodeOK              = 0
	CodeBadRequest      = 40000
	CodeUnauthorized    = 40100
	CodeNotFound        = 40400
	CodeMethodNotAllow  = 40500
	CodeTooManyRequests = 42900
	CodeInternal        = 50000

	// 业务错误码
	CodeSourceNotFound     = 40001
	CodeVideoNotFound      = 40002
	CodeInvalidParam       = 40003
	CodeCredentialEmpty    = 40004
	CodeCredentialExpired  = 40005
	CodeTaskBusy           = 40006
	CodeScannerUnavailable = 40007
)

// apiOK 成功响应
func apiOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{
		Code:    CodeOK,
		Data:    data,
		Message: "ok",
	})
}

// apiError 错误响应
func apiError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")

	// 根据错误码推断 HTTP 状态码
	httpStatus := http.StatusInternalServerError
	switch {
	case code >= 50000:
		httpStatus = http.StatusInternalServerError
	case code >= 42900:
		httpStatus = http.StatusTooManyRequests
	case code >= 40500:
		httpStatus = http.StatusMethodNotAllowed
	case code >= 40400:
		httpStatus = http.StatusNotFound
	case code >= 40100:
		httpStatus = http.StatusUnauthorized
	case code >= 40000:
		httpStatus = http.StatusBadRequest
	}

	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(Response{
		Code:    code,
		Data:    nil,
		Message: msg,
	})
}

// apiPaginated 分页响应
func apiPaginated(w http.ResponseWriter, items interface{}, total, page, pageSize int) {
	apiOK(w, PaginatedData{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// parseJSON 解析 JSON 请求体
func parseJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}
