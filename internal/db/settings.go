package db

import "database/sql"

func (d *DB) GetSetting(key string) (string, error) {
	var value string
	err := d.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (d *DB) SetSetting(key, value string) error {
	_, err := d.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

// DeleteSettingsByPrefix 删除所有以 prefix 开头的 settings 条目
func (d *DB) DeleteSettingsByPrefix(prefix string) error {
	_, err := d.Exec("DELETE FROM settings WHERE key LIKE ?", prefix+"%")
	return err
}
