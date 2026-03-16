package douyin

// SignPoolStats 签名池统计信息
type SignPoolStats struct {
	Size     int   `json:"size"`
	Created  int64 `json:"created"`
	Recycled int64 `json:"recycled"`
}

// GetSignPoolStats 返回 X-Bogus 签名池统计
func GetSignPoolStats() *SignPoolStats {
	pool, err := getSignPool()
	if err != nil || pool == nil {
		return nil
	}
	created, recycled := pool.stats()
	return &SignPoolStats{
		Size:     pool.size,
		Created:  created,
		Recycled: recycled,
	}
}

// GetABogusPoolStats 返回 a_bogus 签名池统计
func GetABogusPoolStats() *SignPoolStats {
	pool, err := getABogusPool()
	if err != nil || pool == nil {
		return nil
	}
	created, recycled := pool.stats()
	return &SignPoolStats{
		Size:     pool.size,
		Created:  created,
		Recycled: recycled,
	}
}
