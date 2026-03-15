package api

import (
	"net/http"
	"strconv"
	"strings"
)

// Pagination 分页参数
type Pagination struct {
	Page     int
	PageSize int
	Offset   int
	Sort     string   // 字段名
	Order    string   // asc | desc
}

// ParsePagination 从请求中解析分页参数
func ParsePagination(r *http.Request) Pagination {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	sort := r.URL.Query().Get("sort")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	p := Pagination{
		Page:     page,
		PageSize: pageSize,
		Offset:   (page - 1) * pageSize,
	}

	// 解析排序：created_desc → Sort=created, Order=desc
	if sort != "" {
		parts := strings.Split(sort, "_")
		if len(parts) >= 2 {
			p.Order = parts[len(parts)-1]
			p.Sort = strings.Join(parts[:len(parts)-1], "_")
		} else {
			p.Sort = sort
			p.Order = "desc"
		}
		// 安全检查：只允许 asc/desc
		if p.Order != "asc" && p.Order != "desc" {
			p.Order = "desc"
		}
	}

	return p
}
