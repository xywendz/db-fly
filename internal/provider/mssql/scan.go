package mssql

import (
	"database/sql"
)

// scanAllStrings 把任意结果集读成字符串矩阵，NULL 表示为空串。
func scanAllStrings(rows *sql.Rows) ([][]string, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out [][]string
	for rows.Next() {
		values := make([]any, len(cols))
		raws := make([]*sql.RawBytes, len(cols))
		for i := range raws {
			raws[i] = new(sql.RawBytes)
			values[i] = raws[i]
		}
		if err := rows.Scan(values...); err != nil {
			return nil, err
		}
		record := make([]string, len(cols))
		for i, p := range raws {
			if *p != nil {
				record[i] = string(*p)
			}
		}
		out = append(out, record)
	}
	return out, rows.Err()
}
