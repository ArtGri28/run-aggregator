// Запросы к /api/v1: initData — только в заголовке Authorization (никогда в
// URL), ошибки сервера {"error":…} — исключение ApiError, таймаут через
// AbortController. Повторов нет: решение о повторе принимает экран.

const TIMEOUT_MS = 10000;

export class ApiError extends Error {
  constructor(status, code, retryAfter) {
    super(code);
    this.status = status;
    this.code = code;
    this.retryAfter = retryAfter;
  }
}

function initData() {
  const tg = window.Telegram && window.Telegram.WebApp;
  return tg && tg.initData ? tg.initData : '';
}

// request — запрос /api/v1/<path>?<params> без тела; пустые параметры не
// передаются. Ответ 204 — null.
async function request(method, path, params = {}) {
  const url = new URL('/api/v1/' + path, window.location.origin);
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') url.searchParams.set(k, String(v));
  }
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), TIMEOUT_MS);
  let res;
  try {
    res = await fetch(url, { method, headers: { Authorization: 'tma ' + initData() }, signal: ctrl.signal });
  } catch (e) {
    throw new ApiError(0, e && e.name === 'AbortError' ? 'timeout' : 'network');
  } finally {
    clearTimeout(timer);
  }
  let body = null;
  if (res.status !== 204) {
    try { body = await res.json(); } catch { body = null; }
  }
  if (!res.ok) {
    const retry = Number(res.headers.get('Retry-After')) || 0;
    throw new ApiError(res.status, (body && body.error) || 'internal', retry);
  }
  return body;
}

// get — чтение; put и del — запись избранного (без тела, идемпотентны).
export const get = (path, params) => request('GET', path, params);
export const put = (path) => request('PUT', path);
export const del = (path) => request('DELETE', path);

// meta — справочники: один запрос на сеанс, при ошибке следующий вызов
// запрашивает снова.
let metaPromise = null;
export function meta() {
  if (!metaPromise) {
    metaPromise = get('meta').catch((e) => { metaPromise = null; throw e; });
  }
  return metaPromise;
}
