package db

import "time"

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
	return people, nil
}
