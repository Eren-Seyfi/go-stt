// Small toast notification store shared by both windows.
export const toasts = $state([]);
let next = 0;

export function notify(message, kind = 'info') {
  const id = ++next;
  toasts.push({id, message, kind});
  setTimeout(() => {
    const i = toasts.findIndex((t) => t.id === id);
    if (i >= 0) toasts.splice(i, 1);
  }, kind === 'error' ? 6000 : 3000);
}

export const errMsg = (e) => String(e?.message ?? e);
