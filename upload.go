package filestorage

import (
	"context"
	"database/sql"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lovego/addrs"
	"github.com/lovego/errs"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

func UploadImages(req *http.Request, lang string) ([]string, error) {
	if err := req.ParseMultipartForm(10 * (1 << 20)); err != nil {
		return nil, err
	}
	files := req.MultipartForm.File["file"]
	if len(files) == 0 {
		return nil, errs.New("args-err", "no files")
	}
	q := req.URL.Query()

	bucket, err := GetBucket(q.Get("bucket"))
	if err != nil {
		return nil, err
	}
	return bucket.Upload(nil, imageChecker{lang}.Check, q.Get("linkObject"), files...)
}

// Upload files, if object is not empty, the files are linked to it.
func (b *Bucket) Upload(
	db DB, fileCheck func(string, int64) error, object string, fileHeaders ...*multipart.FileHeader,
) ([]string, error) {
	var files = make([]File, len(fileHeaders))
	for i := range fileHeaders {
		f, err := fileHeaders[i].Open()
		if err != nil {
			return nil, err
		}
		defer f.Close()
		files[i].IO = f
		files[i].Size = fileHeaders[i].Size
	}
	return b.Save(db, fileCheck, object, files...)
}

// File reprents the file to store.
type File struct {
	IO   io.ReadSeeker
	Size int64
}

// Save file into storage.
func (b *Bucket) Save(
	db DB, fileCheck func(string, int64) error, object string, files ...File,
) (fileHashes []string, err error) {
	if len(files) == 0 {
		return nil, nil
	}
	err = runInTx(db, func(tx DB) error {
		hashes, err := b.save(tx, fileCheck, object, files)
		if err != nil {
			return err
		}
		fileHashes = hashes
		return nil
	})
	return
}

func (b *Bucket) save(
	db DB, fileCheck func(string, int64) error, object string, files []File,
) ([]string, error) {
	records, err := b.createFileRecords(db, files, fileCheck)
	if err != nil {
		return nil, err
	}
	var hashes []string
	for i := range records {
		hashes = append(hashes, records[i].Hash)
	}
	if object != "" {
		if err := b.Link(db, object, hashes...); err != nil {
			return nil, err
		}
	}
	for i := range records {
		if err := b.saveFile(records[i].File, records[i].Hash); err != nil {
			return nil, err
		}
	}
	return hashes, nil
}

func (b *Bucket) saveFile(file io.Reader, hash string) error {
	var srcPath string
	var destPath = filepath.Join(b.Dir, b.FilePath(hash))
	if b.localMachine {
		if err := b.writeFile(file, destPath); err != nil {
			return err
		}
		srcPath = destPath
	} else {
		tempFile, err := b.writeTempFile(file)
		if err != nil {
			return err
		}
		srcPath = tempFile
	}
	for _, addr := range b.otherMachines {
		if err := exec.Command("scp", srcPath, addr+":"+destPath).Run(); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bucket) writeFile(file io.Reader, destPath string) error {
	if _, err := os.Stat(destPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	destFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer destFile.Close()
	_, err = io.Copy(destFile, file)
	return err
}

func (b *Bucket) writeTempFile(file io.Reader) (string, error) {
	temp, err := ioutil.TempFile("", "fs_")
	if err != nil {
		return "", err
	}
	defer temp.Close()
	if _, err := io.Copy(temp, file); err != nil {
		return "", err
	}
	return temp.Name(), nil
}

func (b *Bucket) parseMachines() error {
	var user string
	if b.ScpUser != "" {
		user = b.ScpUser + "@"
	}
	for _, addr := range b.Machines {
		if ok, err := addrs.IsLocalhost(addr); err != nil {
			return err
		} else if ok {
			b.localMachine = true
		} else {
			b.otherMachines = append(b.otherMachines, user+addr)
		}
	}
	return nil
}

// FilePath returns the file path to store on disk.
func (b *Bucket) FilePath(hash string) string {
	var path string
	var i uint8
	for ; i < b.DirDepth; i++ {
		path = filepath.Join(path, hash[i:i+1])
	}
	return filepath.Join(path, hash)
}

func runInTx(db DB, work func(DB) error) error {
	if sqldb, ok := db.(*sql.DB); ok {
		tx, err := sqldb.BeginTx(context.Background(), nil)
		if err != nil {
			return err
		}
		defer func() {
			if err := recover(); err != nil {
				_ = tx.Rollback()
				panic(err)
			}
		}()
		if err := work(tx); err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}
	return work(db)
}

type imageChecker struct {
	lang string
}

func (img imageChecker) Check(contentType string, size int64) error {
	if !strings.HasPrefix(contentType, "image/") {
		return img.fileTypeError(contentType)
	}

	if size > 2*(1<<20) {
		return img.fileSizeError(size)
	}
	return nil
}

func (img imageChecker) fileTypeError(typ string) error {
	switch img.lang {
	case "zh", "cn":
		return errs.Newf("args-err", "文件类型(%s)不是图片.", typ)
	default:
		return errs.Newf("args-err", "file type(%s) is not an image.", typ)
	}
}

var printer = message.NewPrinter(language.English)

func (img imageChecker) fileSizeError(size int64) error {
	s := printer.Sprintf("%d", size)
	switch img.lang {
	case "zh", "cn":
		return errs.Newf("args-err", "文件大小(%s)不能超过2兆.", s)
	default:
		return errs.Newf("args-err", "file size(%s) cann't exceed 2MB.", s)
	}
}
