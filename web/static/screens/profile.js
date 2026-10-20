// Профиль: вкладки «Профиль» и «Мой календарь» (A-1). «Профиль» — число
// избранного, ответы анкеты GET /profile только для чтения и текущие фильтры
// каталога; анкета заполняется и меняется в боте, кнопка закрывает Mini App и
// возвращает в чат. «Мой календарь» (A-2) — избранные будущие старты по
// месяцам и запросы файла .ics и ссылки календаря: их присылает бот.

import { del, get, meta, put } from '../api.js';
import { el, errorText, heading, showError, toChat } from '../ui.js';
import { glyph } from '../icons.js';
import { row } from '../row.js';
import { filterTags, loadFilters, toQuery } from './filters.js';

const MONTHS = ['Январь', 'Февраль', 'Март', 'Апрель', 'Май', 'Июнь', 'Июль', 'Август', 'Сентябрь', 'Октябрь', 'Ноябрь', 'Декабрь'];

export const back = '#/';

function tile(value, label, href) {
  const cls = typeof value === 'number' ? 'num' : 'val';
  return el(href ? 'a' : 'div', { class: 'tile', href }, el('span', { class: cls, text: String(value) }), el('span', { class: 'sub', text: label }));
}

function toBot(text) {
  return el('button', { type: 'button', class: 'btn btn-block', text, onclick: () => window.Telegram.WebApp.close() });
}

function tabs(tab) {
  const a = (href, text, on) => el('a', { href, text, class: 'seg-item', role: 'tab', 'aria-selected': String(on), 'aria-current': on ? 'page' : null });
  return el('nav', { class: 'seg', role: 'tablist' }, a('#/profile', 'Профиль', tab !== 'calendar'), a('#/profile/calendar', 'Мой календарь', tab === 'calendar'));
}

async function myCalendar(box) {
  let fav;
  let m;
  try {
    [fav, m] = await Promise.all([get('favorites', { limit: 50 }), meta()]);
  } catch (e) {
    if (box.isConnected) showError(box, e, () => render(box, 'calendar'));
    return;
  }
  if (!box.isConnected) return;
  if (!fav.total) {
    box.append(el('div', { class: 'notice' },
      el('p', { text: 'Здесь пока пусто. Добавьте старты в избранное — они появятся здесь по датам.' }),
      el('a', { class: 'btn btn-block', href: '#/catalog', text: 'К каталогу' })));
    return;
  }
  let month = '';
  let list = null;
  for (const it of fav.items) {
    const [y, mo] = it.start_date.split('-').map(Number);
    const title = `${MONTHS[mo - 1]} ${y}`;
    if (title !== month) {
      month = title;
      list = el('ul', { class: 'list list-2' });
      box.append(el('h2', { text: title }), list);
    }
    list.append(row(it, m, { fav: true, onChange: () => { if (box.isConnected) render(box, 'calendar'); } }));
  }
  box.append(...toChat('📅 Добавить все в календарь телефона', 'calendar/file'),
    ...toChat('🔗 Подключить к календарю телефона', 'calendar/link'));
}

export async function render(box, tab) {
  box.replaceChildren(heading('Твой спортивный сезон', 'Профиль'), tabs(tab));
  if (tab === 'calendar') {
    await myCalendar(box);
    return;
  }
  let p;
  let fav;
  let m;
  try {
    [p, fav, m] = await Promise.all([get('profile'), get('favorites', { limit: 1 }), meta()]);
  } catch (e) {
    if (box.isConnected) showError(box, e, () => render(box));
    return;
  }
  if (!box.isConnected) return;

  const who = el('div', { class: 'card who' }, el('span', { class: 'plaque plaque-lg plaque-accent' }, glyph('user')));
  const tiles = el('div', { class: 'tiles' }, tile(fav.total, 'В избранном', '#/favorites'));
  box.append(who, tiles);
  if (p.exists) {
    const label = (o, empty) => (o ? o.label : empty);
    who.append(el('p', { class: 'hint', text: 'Ответы анкеты меняются в боте, в разделе «Мой профиль».' }));
    tiles.append(
      tile(label(p.gender, 'не указан'), 'Пол'),
      tile(label(p.age_group, 'не указан'), 'Возраст'),
      tile(label(p.country, 'не указана'), 'Страна'),
      tile(p.city || 'не указан', 'Город'),
      tile(p.sports.length ? p.sports.map((s) => s.label).join(', ') : 'не указан', 'Спорт'));
    box.append(toBot('Изменить в боте'));
  } else {
    who.append(el('p', { text: 'Анкета заполняется в боте.' }),
      el('p', { class: 'hint', text: 'Вернитесь в чат с ботом и отправьте команду /start.' }));
    box.append(toBot('Вернуться в бот'));
  }

  box.append(el('h2', { text: 'Предпочтения' }),
    el('div', { class: 'card' },
      el('p', { class: 'hint', text: 'Фильтры каталога' }),
      filterTags(loadFilters(), m),
      el('a', { class: 'btn btn-primary btn-block', href: '#/filters', text: 'Настроить фильтры' })));
  const sub = el('div', { class: 'card' });
  box.append(el('h2', { text: 'Подписка на новые старты' }), sub);
  subscription(sub, m);
}

const SUB_OFF = 'Раз в неделю, по понедельникам, бот пришлёт старты, которые появились в каталоге по фильтрам каталога. Если новых не будет — сообщения не будет.';
const SUB_ON = 'Сообщение приходит по понедельникам после 10:00 по Москве — только если появились новые старты по этим фильтрам. Отписаться можно здесь или кнопкой в самом сообщении.';

// subQuery — фильтры каталога для подписки: без дат.
function subQuery() {
  const q = toQuery(loadFilters());
  delete q.periods;
  return q;
}

// subscription — блок подписки (A-3): одна подписка, общая с ботом.
async function subscription(card, m) {
  let s;
  try {
    s = await get('subscription', subQuery());
  } catch (e) {
    if (card.isConnected) showError(card, e, () => subscription(card, m));
    return;
  }
  if (!card.isConnected) return;
  const note = el('p', { class: 'hint', role: 'status' });
  const act = (text, cls, call, done) => el('button', {
    type: 'button', class: cls, text,
    onclick: async (ev) => {
      const btn = ev.currentTarget;
      btn.disabled = true;
      try {
        await call();
        await subscription(card, m);
        if (done) card.append(el('p', { class: 'hint', role: 'status', text: done }));
      } catch (e) {
        note.textContent = errorText(e, 'Не удалось сохранить. Повторите позже.');
        btn.disabled = false;
      }
    },
  });
  const subscribe = () => put('subscription', subQuery());
  if (!s.on) {
    card.replaceChildren(el('p', { text: '🔔 ' + SUB_OFF }), allTags(loadFilters(), m),
      act('🔔 Подписаться по фильтрам каталога', 'btn btn-primary btn-block', subscribe, '🔔 Подписка включена. Новые старты — по понедельникам'), note);
    return;
  }
  const f = { ...s.filters, cityName: s.filters.city_name, periods: [] };
  card.replaceChildren(el('p', { text: '🔔 Подписка на новые старты: включена' + (s.same ? '' : ' (по прежним фильтрам)') }),
    el('p', { class: 'hint', text: SUB_ON }), allTags(f, m));
  if (!s.same) card.append(act('Обновить по фильтрам каталога', 'btn btn-primary btn-block', subscribe));
  card.append(act('🔕 Отписаться', 'btn btn-block', () => del('subscription')), note);
}

// allTags — теги фильтров; без вида спорта — «Все виды спорта».
function allTags(f, m) {
  const tags = filterTags(f, m);
  if (!f.sport) tags.prepend(el('span', { class: 'tag', text: 'Все виды спорта' }));
  return tags;
}
