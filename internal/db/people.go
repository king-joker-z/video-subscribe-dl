package db

import (
	"fmt"
	"time"
)

// PersonWithCount 带视频数量的 Person
type PersonWithCount struct {
	ID         int64     `json:"id"`
	MID        string    `json:"mid"`
	Name       string    `json:"name"`
	Avatar     string    `json:"avatar"`
	VideoCount int       `json:"video_count"`
	CreatedAt  time.Time `json:"created_at"`
}

func (d *DB) UpsertPerson(mid, name, avatar string) error {
	_, err := d.Exec(`
		INSERT INTO people (mid, name, avatar) VALUES (?, ?, ?)
		ON CONFLICT(mid) DO UPDATE SET name = excluded.name, avatar = excluded.avatar
	`, mid, name, avatar)
	return err
}

func (d *DB) GetPeople() ([]Person, error) {
	rows, err := d.Query("SELECT id, mid, name, COALESCE(avatar,''), created_at FROM people")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var people []Person
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.MID, &p.Name, &p.Avatar, &p.CreatedAt); err != nil {
			return nil, err
		}
		people = append(people, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return people, nil
}

// GetPeopleWithVideoCount 获取 UP 主列表及各自的视频数量
func (d *DB) GetPeopleWithVideoCount() ([]PersonWithCount, error) {
	rows, err := d.Query(`
		SELECT p.id, p.mid, p.name, COALESCE(p.avatar,''), p.created_at,
		       COALESCE(vc.cnt, 0) AS video_count
		FROM people p
		LEFT JOIN (
			SELECT s.name AS source_name, COUNT(*) AS cnt
			FROM downloads dl
			JOIN sources s ON s.id = dl.source_id
			WHERE dl.status IN ('completed','relocated')
			GROUP BY s.name
		) vc ON vc.source_name = p.name
		ORDER BY video_count DESC, p.name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var people []PersonWithCount
	for rows.Next() {
		var p PersonWithCount
		if err := rows.Scan(&p.ID, &p.MID, &p.Name, &p.Avatar, &p.CreatedAt, &p.VideoCount); err != nil {
			return nil, err
		}
		people = append(people, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return people, nil
}

// DeleteUploaderData 删除指定 UP 主的所有数据：downloads 记录 + people 记录
// 不删除本地文件，仅清除 DB 数据，让 UI 上不再显示该 UP 主
func (d *DB) DeleteUploaderData(name string) (int64, error) {
	var cnt int
	d.QueryRow("SELECT COUNT(*) FROM downloads WHERE uploader = ? AND status = 'downloading'", name).Scan(&cnt)
	if cnt > 0 {
		return 0, fmt.Errorf("该 UP 主有 %d 个任务正在下载，请等待完成后再删除", cnt)
	}
	// 删除 downloads（所有状态）
	res, err := d.Exec("DELETE FROM downloads WHERE uploader = ?", name)
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()
	// 删除 people 记录（按名称）
	d.Exec("DELETE FROM people WHERE name = ?", name)
	return affected, nil
}

// GetPersonByMID 按 MID 获取 UP 主信息
func (d *DB) GetPersonByMID(mid int64) (*Person, error) {
	var p Person
	err := d.QueryRow("SELECT id, mid, name, COALESCE(avatar,''), created_at FROM people WHERE mid = ?",
		fmt.Sprintf("%d", mid)).Scan(&p.ID, &p.MID, &p.Name, &p.Avatar, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPeopleByName 按名称获取 UP 主信息
func (d *DB) GetPeopleByName(name string) (*Person, error) {
	var p Person
	err := d.QueryRow("SELECT id, mid, name, COALESCE(avatar,''), created_at FROM people WHERE name = ?", name).Scan(&p.ID, &p.MID, &p.Name, &p.Avatar, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
