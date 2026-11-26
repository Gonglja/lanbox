package store

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// CreateInput 是新建内容的输入（附件随后通过 AddFile 追加）。
type CreateInput struct {
	Text      string
	CreatedBy string
	SourceIP  string
}

// ListQuery 是列表/搜索参数。
type ListQuery struct {
	Limit  int
	Before string
	Query  string
	Todo   bool
}

// ListResult 是分页结果。NextCursor 为空表示没有更多。
type ListResult struct {
	Items      []*Item `json:"items"`
	NextCursor string  `json:"next_cursor"`
}

// Create 新建一条内容（可无文本，由调用方决定是否合法）。
func (s *Store) Create(in CreateInput) (*Item, error) {
	text := strings.TrimSpace(in.Text)
	if s.lims().maxTextBytes > 0 && len(text) > s.lims().maxTextBytes {
		return nil, ErrTextTooLong
	}
	it := &Item{
		ID:        NewID(),
		CreatedAt: time.Now().UTC(),
		CreatedBy: in.CreatedBy,
		SourceIP:  in.SourceIP,
		Text:      text,
		Files:     []File{},
	}
	if err := os.MkdirAll(s.filesDir(it.ID), 0o750); err != nil {
		return nil, fmt.Errorf("创建内容目录失败: %w", err)
	}
	if err := s.saveMeta(it); err != nil {
		os.RemoveAll(s.itemDir(it.ID))
		return nil, err
	}
	s.mu.Lock()
	s.index[it.ID] = it
	s.mu.Unlock()
	return it.clone(), nil
}

// Get 返回内容副本。
func (s *Store) Get(id string) (*Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.index[id]
	if !ok {
		return nil, ErrNotFound
	}
	return it.clone(), nil
}

// List 按 ID 倒序返回内容，支持游标、搜索与"未取走"过滤。
func (s *Store) List(q ListQuery) (ListResult, error) {
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	needle := strings.ToLower(strings.TrimSpace(q.Query))

	s.mu.RLock()
	ids := s.sortedIDsLocked()
	res := ListResult{Items: []*Item{}}
	for _, id := range ids {
		if q.Before != "" && id >= q.Before {
			continue
		}
		it := s.index[id]
		if q.Todo && it.Taken {
			continue
		}
		if needle != "" && !it.match(needle) {
			continue
		}
		if len(res.Items) >= q.Limit {
			res.NextCursor = res.Items[len(res.Items)-1].ID
			break
		}
		res.Items = append(res.Items, it.clone())
	}
	s.mu.RUnlock()
	return res, nil
}

// SetText 覆盖内容文本（用于先收附件、后拿到文本的上传顺序）。
func (s *Store) SetText(id, text string) (*Item, error) {
	text = strings.TrimSpace(text)
	if s.lims().maxTextBytes > 0 && len(text) > s.lims().maxTextBytes {
		return nil, ErrTextTooLong
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.index[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := it.clone()
	cp.Text = text
	if err := s.saveMeta(cp); err != nil {
		return nil, err
	}
	s.index[id] = cp
	return cp.clone(), nil
}

// SetTaken 标记/取消"已取走"。
func (s *Store) SetTaken(id string, taken bool, by string) (*Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.index[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := it.clone()
	if taken {
		now := time.Now().UTC()
		cp.Taken = true
		cp.TakenAt = &now
		cp.TakenBy = by
	} else {
		cp.Taken = false
		cp.TakenAt = nil
		cp.TakenBy = ""
	}
	if err := s.saveMeta(cp); err != nil {
		return nil, err
	}
	s.index[id] = cp
	return cp.clone(), nil
}

// Delete 直接删除整条内容（连同目录）。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.index[id]; !ok {
		return ErrNotFound
	}
	if err := os.RemoveAll(s.itemDir(id)); err != nil {
		return fmt.Errorf("删除内容失败: %w", err)
	}
	delete(s.index, id)
	return nil
}
