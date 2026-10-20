// Общие помощники экранов: создание узлов (только textContent и атрибуты —
// данные каталога приходят из внешних источников), даты, флаги, дистанции,
// заголовок экрана, короткие сообщения, заглушка вне Telegram и показ ошибок API.

import { ApiError, post } from './api.js';

// el — узел с атрибутами и детьми. Ключ "text" — textContent, "on<event>" —
// обработчик, значение true — пустой атрибут, null/false — атрибут не ставится.
export function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === null || v === undefined || v === false) continue;
    if (k === 'text') node.textContent = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v === true ? '' : v);
  }
  for (const c of children.flat()) {
    if (c !== null && c !== undefined && c !== false) node.append(c);
  }
  return node;
}

// link — внешняя ссылка; принимается только http(s), остальное — просто текст.
export function link(url, text) {
  if (!/^https?:\/\/\S+$/i.test(url)) return el('span', { text });
  return el('a', { href: url, target: '_blank', rel: 'noopener noreferrer', text });
}

const MONTHS = ['января', 'февраля', 'марта', 'апреля', 'мая', 'июня',
  'июля', 'августа', 'сентября', 'октября', 'ноября', 'декабря'];

// ruDate — «2 октября 2026» из YYYY-MM-DD.
export function ruDate(iso) {
  const [y, m, d] = iso.split('-').map(Number);
  return `${d} ${MONTHS[m - 1]} ${y}`;
}

// MON — месяц тремя буквами для плашки даты.
export const MON = ['ЯНВ', 'ФЕВ', 'МАР', 'АПР', 'МАЙ', 'ИЮН', 'ИЮЛ', 'АВГ', 'СЕН', 'ОКТ', 'НОЯ', 'ДЕК'];

// flag — флаг из кода ISO alpha-2 (региональные индикаторы), '' для иного.
export function flag(code) {
  if (!/^[A-Z]{2}$/.test(code || '')) return '';
  return String.fromCodePoint(...[...code].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65));
}

// km — километры: целые без дроби, иначе одна цифра после запятой (как у бота).
export function km(m) {
  return m % 1000 === 0 ? String(m / 1000) : (m / 1000).toFixed(1).replace('.', ',');
}

// plural — форма слова по числу: plural(5, 'старт', 'старта', 'стартов').
export function plural(n, one, few, many) {
  const a = Math.abs(n) % 100;
  const b = a % 10;
  if (a > 10 && a < 20) return many;
  if (b === 1) return one;
  if (b >= 2 && b <= 4) return few;
  return many;
}

// events — «1 событие», «2 события», «5 событий».
export function events(n) {
  return `${n} ${plural(n, 'событие', 'события', 'событий')}`;
}

// heading — заголовок экрана: надзаголовок, заголовок и действия справа.
export function heading(eyebrow, title, ...actions) {
  return el('div', { class: 'head' },
    el('div', {}, el('p', { class: 'eyebrow', text: eyebrow }), el('h1', { text: title })),
    el('div', { class: 'actions' }, ...actions));
}

// toast — короткое сообщение над нижней панелью; гаснет само.
let toastTimer = 0;
export function toast(text) {
  let node = document.getElementById('toast');
  if (!node) {
    node = el('div', { id: 'toast', class: 'toast', role: 'status' });
    document.body.append(node);
  }
  node.textContent = text;
  node.hidden = !text;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { node.hidden = true; }, 5000);
}

export const STATUS_LABEL = { cancelled: 'Отменён', postponed: 'Перенесён' };

// statusBadge — пометка отменённого/перенесённого старта или null.
export function statusBadge(status) {
  const label = STATUS_LABEL[status];
  return label ? el('span', { class: 'badge', text: label }) : null;
}

// stub — заглушка: страница открыта не из Telegram или initData отклонён.
export function stub(root) {
  document.body.classList.add('stubbed'); // без шапки и нижней панели
  root.replaceChildren(el('div', { class: 'stub screen' },
    el('div', { class: 'mark', text: '🏁' }),
    el('h1', { text: 'Где старт?' }),
    el('p', { class: 'slogan', text: 'Все старты в одном месте.' }),
    el('p', { text: 'Откройте приложение из Telegram.' })));
}

// errorText — текст ошибки запроса; fallback — для прочих ошибок. 401 —
// заглушка на всю страницу и пустая строка.
export function errorText(err, fallback) {
  const e = err instanceof ApiError ? err : new ApiError(0, 'network');
  if (e.status === 401) {
    stub(document.getElementById('app'));
    return '';
  }
  if (e.status === 429) {
    return e.retryAfter ? `Слишком много запросов. Повторите через ${e.retryAfter} с.` : 'Слишком много запросов. Повторите позже.';
  }
  if (e.code === 'timeout') return 'Сервер не ответил вовремя.';
  if (e.status === 404) return 'Событие больше не доступно.';
  return fallback;
}

// showError — сообщение об ошибке запроса в box; retry — повтор по кнопке
// (без автоматических повторов).
export function showError(box, err, retry) {
  const e = err instanceof ApiError ? err : new ApiError(0, 'network');
  const text = errorText(e, 'Не удалось загрузить данные.');
  if (!text) return;
  const notice = el('div', { class: 'notice' }, el('p', { text }));
  if (e.status === 404) {
    notice.append(el('a', { class: 'btn btn-block', href: '#/catalog', text: 'К каталогу' }));
  } else if (retry) {
    notice.append(el('button', { class: 'btn btn-block', type: 'button', text: 'Повторить', onclick: () => { notice.remove(); retry(); } }));
  }
  box.append(notice);
}

// toChat — кнопка запроса, который выполнит бот (файл .ics, ссылка
// календаря), и строка ответа под ней.
export function toChat(text, path, params) {
  const note = el('p', { class: 'hint', role: 'status' });
  const btn = el('button', { type: 'button', class: 'btn btn-block', text });
  btn.addEventListener('click', async () => {
    btn.disabled = true;
    try {
      await post(path, params);
      note.textContent = path === 'calendar/link' ? 'Ссылка придёт в чат с ботом.' : 'Файл придёт в чат с ботом.';
    } catch (e) {
      note.textContent = e.code === 'empty' ? 'Нет стартов с точной датой.' : errorText(e, 'Не удалось отправить запрос. Повторите позже.');
    }
    btn.disabled = false;
  });
  return [btn, note];
}
