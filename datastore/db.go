package datastore

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	outFileName = "segment"
	Mi          = int64(1024 * 1024)
)

var ErrNotFound = fmt.Errorf("record does not exist")

type hashIndex map[string]int64

type Db struct {
	segments      []*Segment
	activeSegment *Segment
	segmentSize   int64
	dir           string
}

type Segment struct {
	file     *os.File
	filePath string
	offset   int64
	index    hashIndex
}

func Open(dir string, segmentSize int64) (*Db, error) {
	db := &Db{
		segments:    []*Segment{},
		segmentSize: segmentSize,
		dir:         dir,
	}
	err := db.recover()
	if err != nil {
		return nil, err
	}
	if len(db.segments) == 0 {
		segment, err := db.createSegment()
		if err != nil {
			return nil, err
		}
		db.activeSegment = segment
		db.segments = append(db.segments, segment)
	} else {
		db.activeSegment = db.segments[len(db.segments)-1]
	}
	return db, nil
}

func (db *Db) createSegment() (*Segment, error) {
	name := fmt.Sprintf("%s-%d", outFileName, len(db.segments))
	path := filepath.Join(db.dir, name)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return &Segment{
		file:     f,
		filePath: path,
		index:    make(hashIndex),
	}, nil
}

func (db *Db) recover() error {
	files, err := os.ReadDir(db.dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.IsDir() || !filepath.HasPrefix(file.Name(), outFileName) {
			continue
		}
		path := filepath.Join(db.dir, file.Name())
		f, err := os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		segment := &Segment{
			file:     f,
			filePath: path,
			index:    make(hashIndex),
		}
		reader := bufio.NewReader(f)
		for {
			var rec entry
			pos := segment.offset
			n, err := rec.DecodeFromReader(reader)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				f.Close()
				return fmt.Errorf("corrupt segment %s: %w", path, err)
			}
			segment.index[rec.key] = pos
			segment.offset += int64(n)
		}
		db.segments = append(db.segments, segment)
	}
	return nil
}

func (db *Db) Close() error {
	for _, s := range db.segments {
		s.file.Close()
	}
	return nil
}

func (db *Db) Get(key string) (string, error) {
	for i := len(db.segments) - 1; i >= 0; i-- {
		segment := db.segments[i]
		if offset, ok := segment.index[key]; ok {
			f, err := os.Open(segment.filePath)
			if err != nil {
				return "", err
			}
			defer f.Close()
			_, err = f.Seek(offset, io.SeekStart)
			if err != nil {
				return "", err
			}
			var record entry
			if _, err := record.DecodeFromReader(bufio.NewReader(f)); err != nil {
				return "", err
			}
			return record.value, nil
		}
	}
	return "", ErrNotFound
}

func (db *Db) Put(key, value string) error {
	e := entry{key: key, value: value}
	encoded := e.Encode()
	n, err := db.activeSegment.file.Write(encoded)
	if err != nil {
		return err
	}
	db.activeSegment.index[key] = db.activeSegment.offset
	db.activeSegment.offset += int64(n)
	size, err := db.Size()
	if err != nil {
		return err
	}
	if size >= db.segmentSize {
		newSegment, err := db.createSegment()
		if err != nil {
			return err
		}
		db.segments = append(db.segments, newSegment)
		db.activeSegment = newSegment
	}
	return nil
}

func (db *Db) Size() (int64, error) {
	info, err := db.activeSegment.file.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (db *Db) MergeSegments() error {
	mergedSegment, err := db.createSegment()
	if err != nil {
		return err
	}
	mergedIndex := make(hashIndex)
	for _, segment := range db.segments {
		for key, offset := range segment.index {
			if _, exists := mergedIndex[key]; exists {
				continue
			}
			f, err := os.Open(segment.filePath)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.Seek(offset, io.SeekStart)
			if err != nil {
				return err
			}
			var record entry
			if _, err := record.DecodeFromReader(bufio.NewReader(f)); err != nil {
				return err
			}
			n, err := mergedSegment.file.Write(record.Encode())
			if err != nil {
				return err
			}
			mergedIndex[key] = mergedSegment.offset
			mergedSegment.offset += int64(n)
		}
	}
	for _, s := range db.segments {
		s.file.Close()
		os.Remove(s.filePath)
	}
	db.segments = []*Segment{mergedSegment}
	db.activeSegment = mergedSegment
	db.activeSegment.index = mergedIndex
	return nil
}
