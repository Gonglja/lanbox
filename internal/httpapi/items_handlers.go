package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"lanbox/internal/store"
)

func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	res, err := s.store.List(store.ListQuery{
		Limit:  limit,
		Before: q.Get("before"),
		Query:  q.Get("q"),
		Todo:   q.Get("filter") == "todo",
	})
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleCreateItem 以 multipart 流式接收文本与附件。
// 附件可能先于文本到达，因此先建内容（空文本），收完再回填文本。
func (s *Server) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求必须是 multipart/form-data")
		return
	}

	var (
		text  string
		item  *store.Item
		ip    = clientIP(r)
		files int
	)
	cleanup := func() {
		if item != nil {
			if err := s.store.Delete(item.ID); err != nil {
				s.log.Printf("warn: 清理失败的上传 %s 失败: %v", item.ID, err)
			}
			item = nil
		}
	}

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			writeError(w, http.StatusBadRequest, "bad_request", "读取上传数据失败: "+err.Error())
			return
		}
		if part.FormName() == "text" {
			maxTextBytes := s.current().MaxTextBytes
			raw, err := io.ReadAll(io.LimitReader(part, int64(maxTextBytes)+1))
			part.Close()
			if err != nil {
				cleanup()
				writeError(w, http.StatusBadRequest, "bad_request", "读取文本失败")
				return
			}
			if len(raw) > maxTextBytes {
				cleanup()
				writeError(w, http.StatusBadRequest, "text_too_long",
					"文本超过 "+strconv.Itoa(maxTextBytes)+" 字节上限")
				return
			}
			text = string(raw)
			continue
		}
		// 路径只能放在字段名里：multipart.Part.FileName() 会按 RFC 7578 丢掉目录。
		relPath, isFile := uploadPath(part.FormName(), part.FileName())
		if !isFile {
			part.Close()
			continue // 非文件字段，忽略
		}
		if item == nil {
			created, err := s.store.Create(store.CreateInput{CreatedBy: user.Username, SourceIP: ip})
			if err != nil {
				part.Close()
				s.writeStoreErr(w, err)
				return
			}
			item = created
		}
		updated, err := s.store.AddFile(item.ID, relPath, -1, part)
		part.Close()
		if err != nil {
			cleanup()
			s.writeStoreErr(w, err)
			return
		}
		item = updated
		files++
	}

	if item == nil {
		if strings.TrimSpace(text) == "" {
			writeError(w, http.StatusBadRequest, "empty", "请输入文字或选择文件")
			return
		}
		created, err := s.store.Create(store.CreateInput{Text: text, CreatedBy: user.Username, SourceIP: ip})
		if err != nil {
			s.writeStoreErr(w, err)
			return
		}
		item = created
	} else if strings.TrimSpace(text) != "" {
		updated, err := s.store.SetText(item.ID, text)
		if err != nil {
			cleanup()
			s.writeStoreErr(w, err)
			return
		}
		item = updated
	}

	s.log.Printf("info: %s 从 %s 发布内容 %s（%d 个附件）", user.Username, ip, item.ID, files)
	s.broadcast("item.created", map[string]any{"item": item})
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleGetItem(w http.ResponseWriter, r *http.Request) {
	it, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) handlePatchItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Taken *bool `json:"taken"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Taken == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "缺少 taken 字段")
		return
	}
	u := userFrom(r)
	it, err := s.store.SetTaken(r.PathValue("id"), *in.Taken, u.Username)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.broadcast("item.updated", map[string]any{"item": it})
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.Delete(id); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.log.Printf("info: %s 删除内容 %s", userFrom(r).Username, id)
	s.broadcast("item.deleted", map[string]any{"id": id})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	id, fid := r.PathValue("id"), r.PathValue("fid")
	it, err := s.store.DeleteFile(id, fid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.broadcast("item.updated", map[string]any{"item": it})
	w.WriteHeader(http.StatusNoContent)
}
