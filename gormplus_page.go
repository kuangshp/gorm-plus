package gormplus

func DbPage(pageNumber, pageSize int64) (offset int, limit int) {
	if pageNumber == 0 {
		pageNumber = 1
	}
	switch {
	case pageSize > 500:
		pageSize = 500
	case pageSize <= 0:
		pageSize = 10
	}
	return int((pageNumber - 1) * pageSize), int(pageSize)
}
