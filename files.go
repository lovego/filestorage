package filestorage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func (b *Bucket) FileExists(db DB, hash string) (bool, error) {
	if b.FilesTable == "" {
		b.FilesTable = "files"
	}
	var existsHash string
	err := b.getDB(db).QueryRow(fmt.Sprintf(`
	SELECT hash
	FROM %s
	WHERE hash = %s
	`, b.FilesTable, quote(hash),
	)).Scan(&existsHash)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return existsHash != "", nil
}

func (b *Bucket) createFilesTable(db DB) error {
	if b.FilesTable == "" {
		b.FilesTable = "files"
	}
	_, err := b.getDB(db).Exec(fmt.Sprintf(`
	CREATE TABLE IF NOT EXISTS %s (
		hash           text        NOT NULL UNIQUE,
		type           text        NOT NULL,
		size           int8        NOT NULL,
		tranformations jsonb       NOT NULL DEFAULT '{}',
		created_at     timestamptz NOT NULL
	)`, b.FilesTable,
	))
	return err
}

type fileRecord struct {
	Hash string
	Type string
	Size int64
	File io.Reader
}

func (b *Bucket) createFileRecords(
	db DB, files []File, fileCheck func(string, int64) error,
) ([]fileRecord, error) {
	records := make([]fileRecord, 0, len(files))
	for _, file := range files {
		contentType, err := getContentType(file.IO)
		if err != nil {
			return records, err
		}
		if fileCheck != nil {
			if err := fileCheck(contentType, file.Size); err != nil {
				return records, err
			}
		}
		hash, err := getContentHash(file.IO)
		if err != nil {
			return records, err
		}
		records = append(records, fileRecord{
			Hash: hash, Type: contentType, Size: file.Size, File: file.IO,
		})
	}
	if err := b.insertFileRecords(db, records); err != nil {
		return records, err
	}
	return records, nil
}

func (b *Bucket) insertFileRecords(db DB, records []fileRecord) error {
	var values []string
	now := fmtTime(time.Now())
	for _, record := range records {
		values = append(values, fmt.Sprintf("(%s, %s, %d, %s)",
			quote(record.Hash), quote(record.Type), record.Size, now,
		))
	}

	rows, err := b.getDB(db).Query(fmt.Sprintf(`
	INSERT INTO %s
		(hash, type, size, created_at)
	VALUES
		%s
	ON CONFLICT (hash) DO NOTHING
	RETURNING hash
	`, b.FilesTable, strings.Join(values, ",\n\t\t"),
	))
	if err != nil {
		return err
	}
	defer rows.Close()

	var inserted []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return err
		}
		inserted = append(inserted, hash)
	}

	return err
}

func getContentType(file io.ReadSeeker) (string, error) {
	var array [512]byte
	n, err := file.Read(array[:])
	if err != nil {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return http.DetectContentType(array[:n]), nil
}

func getContentHash(file io.ReadSeeker) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)), nil
}

func fmtTime(t time.Time) string {
	return t.Format("'2006-01-02T15:04:05.999999Z07:00'")
}

func quote(s string) string {
	s = strings.Replace(s, "'", "''", -1)
	s = strings.Replace(s, "\000", "", -1)
	return "'" + s + "'"
}
