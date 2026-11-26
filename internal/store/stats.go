package store

// Stats 是全站内容统计快照，供 /api/stats 与 CLI status 使用。
type Stats struct {
	Items int   `json:"items"` // 内容总条数
	Todo  int   `json:"todo"`  // 未取走条数
	Taken int   `json:"taken"` // 已取走条数
	Files int   `json:"files"` // 附件总数
	Bytes int64 `json:"bytes"` // 附件总字节数
}

// Stats 在内存索引上做一次加锁遍历，不做磁盘 IO。
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var st Stats
	st.Items = len(s.index)
	for _, it := range s.index {
		if it.Taken {
			st.Taken++
		} else {
			st.Todo++
		}
		st.Files += len(it.Files)
		for _, f := range it.Files {
			st.Bytes += f.Size
		}
	}
	return st
}

// FreeBytes 返回 items 目录所在文件系统的可用字节数。
func (s *Store) FreeBytes() (int64, error) {
	return s.freeSpace(s.itemsRoot)
}
