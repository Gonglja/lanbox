// EventSource 包装：把事件转给回调，并上报连接状态。
export function connectEvents({ onEvent, onState }) {
  let es = null;
  let closed = false;

  function open() {
    if (closed) return;
    onState('connecting');
    es = new EventSource('/api/events', { withCredentials: true });

    es.addEventListener('open', () => onState('open'));
    es.addEventListener('item.created', (e) => onEvent('item.created', e.data));
    es.addEventListener('item.updated', (e) => onEvent('item.updated', e.data));
    es.addEventListener('item.deleted', (e) => onEvent('item.deleted', e.data));

    es.addEventListener('error', () => {
      // EventSource 自身会重连；只有被彻底关闭（如 401）时才手动重建
      if (es && es.readyState === EventSource.CLOSED) {
        onState('error');
        es.close();
        setTimeout(open, 3000);
      } else {
        onState('error');
      }
    });
  }

  open();
  return {
    close() {
      closed = true;
      if (es) es.close();
    },
  };
}
