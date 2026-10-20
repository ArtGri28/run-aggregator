// Карточка события GET /events/{id}: те же поля, что в карточке бота.
// Шапка — без фото (Q-D2): градиент, значок вида спорта и плашка даты начала.
// Кнопка «Регистрация» — только при registration_url; кнопка избранного
// задаёт состояние (PUT/DELETE идемпотентны) и сразу его показывает.

import { get, meta } from '../api.js';
import { el, flag, km, link, MON, ruDate, showError, statusBadge, toChat } from '../ui.js';
import { icon } from '../icons.js';
import { toggleFav } from '../row.js';

export const back = '#/catalog';

function row(key, ...value) {
  return el('div', { class: 'row' }, el('span', { class: 'k', text: key }), el('span', { class: 'v' }, ...value));
}

// distances — таблетки «10 км»; с подписями — строка с единицей у каждой:
// «5 км (детский) · 10 км».
function distances(list) {
  if (!list.some((d) => d.label)) return el('span', { class: 'tags' }, ...list.map((d) => el('span', { class: 'tag', text: km(d.m) + ' км' })));
  return list.map((d) => km(d.m) + ' км' + (d.label ? ` (${d.label})` : '')).join(' · ');
}

// favControl — кнопка «В избранное» / «Убрать из избранного» и строка
// сообщения об отказе; mark — пометка в шапке карточки.
function favControl(c, m, mark) {
  let fav = c.is_favorite;
  const note = el('p', { class: 'hint', role: 'status' });
  const btn = el('button', { type: 'button', class: 'btn btn-block' });
  const paint = () => {
    btn.textContent = fav ? 'Убрать из избранного' : 'В избранное';
    btn.setAttribute('aria-pressed', String(fav));
    mark.hidden = !fav;
  };
  btn.addEventListener('click', async () => {
    btn.disabled = true;
    const res = await toggleFav(c.id, fav, m.limits.favorites);
    fav = res.fav;
    note.textContent = res.note;
    paint();
    btn.disabled = false;
  });
  paint();
  return [btn, note];
}

// shareButton — «📤 Поделиться»: окно Telegram «Переслать» со ссылкой на
// карточку старта в боте (как кнопка карточки бота, N-1 Phase 20).
function shareButton(bot, c) {
  const link = `https://t.me/${bot}?start=e_${c.id}`;
  const url = 'https://t.me/share/url?url=' + encodeURIComponent(link) + '&text=' + encodeURIComponent(c.title);
  return el('button', { type: 'button', class: 'btn btn-block', text: '📤 Поделиться', onclick: () => window.Telegram.WebApp.openTelegramLink(url) });
}

export async function render(box, id) {
  box.replaceChildren();
  let c;
  let m;
  try {
    [c, m] = await Promise.all([get('events/' + id), meta()]);
  } catch (e) {
    if (box.isConnected) showError(box, e, () => render(box, id));
    return;
  }
  if (!box.isConnected) return;
  const sport = m.sports.find((s) => s.code === c.sport);
  // У небегового события дисциплина «другое» не называется: «Велоспорт».
  const kind = sport && sport.code !== 'running'
    ? sport.title + (c.type !== 'other' ? ' · ' + c.type_label : '')
    : c.type_label;

  const [y, mo, d] = c.start_date.split('-').map(Number);
  const badge = statusBadge(c.status);
  box.append(el('div', { class: 'ev-hero' }, icon(c.sport, c.type),
    el('span', { class: 'stamp', text: `${d} ${MON[mo - 1]} ${y}` }),
    el('div', {}, el('h2', {}, badge, badge ? ' ' : null, c.title), el('p', { class: 'kind', text: kind }))));

  const mark = el('p', { class: 'fav', text: '♥ В избранном' });
  const card = el('div', { class: 'card' }, mark);
  box.append(card);

  let when = ruDate(c.start_date);
  if (c.end_date && c.end_date !== c.start_date) when += ' – ' + ruDate(c.end_date);
  const rows = el('div', { class: 'rows' }, row('Дата', when));
  if (c.start_time) rows.append(row('Старт', `${c.start_time} (местное время)`));
  rows.append(row('Страна', [flag(c.country_code), c.country_name].filter(Boolean).join(' ')));
  rows.append(row('Место', [c.city || 'город не указан', c.address].filter(Boolean).join(', ')));
  if (c.distances.length) rows.append(row('Дистанции', distances(c.distances)));
  card.append(rows);

  if (c.description) card.append(el('p', { class: 'desc', text: c.description }));

  if (c.sources.length) {
    const links = [];
    c.sources.forEach((s, i) => {
      if (i) links.push(', ');
      links.push(s.url ? link(s.url, s.name) : s.name);
    });
    card.append(row(c.sources.length > 1 ? 'Источники' : 'Источник', ...links));
  }

  if (c.registration_url) {
    const a = link(c.registration_url, 'Регистрация');
    if (a.tagName === 'A') {
      a.className = 'btn btn-primary btn-block';
      box.append(a);
    }
  }
  box.append(...favControl(c, m, mark));
  if (!c.date_tbd) box.append(...toChat('📅 В календарь', 'calendar/file', { event: c.id }));
  if (m.bot) box.append(shareButton(m.bot, c));
  box.append(el('a', { class: 'btn btn-block', href: '#/catalog', text: 'К каталогу' }));
}
