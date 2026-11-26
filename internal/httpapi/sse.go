package httpapi

import "sync"

// Event 是一条 SSE 事件。Data 已是 JSON 文本。
type Event struct {
	Type string
	Data string
}

// Hub 维护 SSE 订阅者集合。
type Hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// NewHub 创建空集线器。
func NewHub() *Hub {
	return &Hub{subs: map[chan Event]struct{}{}}
}

// Subscribe 返回事件通道与取消函数。
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

// Broadcast 非阻塞广播；消费过慢的订阅者会被断开。
func (h *Hub) Broadcast(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// Count 返回当前订阅者数量。
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Close 关闭全部订阅者（优雅退出用）。
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		delete(h.subs, ch)
		close(ch)
	}
}
