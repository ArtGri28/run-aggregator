// Карточка события GET /events/{id}: те же поля, что в карточке бота.
// Кнопка «Регистрация» — только при registration_url; кнопка избранного
// задаёт состояние (PUT/DELETE идемпотентны) и сразу его показывает.

import { del, get, meta, put } from '../api.js';
import { el, errorText, flag, km, link, ruDate, showError, statusBadge } from '../ui.js';
import { plaque } from '../icons.js';

export const back = '#/catalog';

function row(key, ...value) {
  return el('div', { class: 'row' }, el('span', { class: 'k', text: key }), el('span', { class: 'v' }, ...value));
}

// distances — «3 · 10 · 21 км»; с подписями — единица у каждой: «5 км (детский) · 10 км».
function distances(list) {
  const labelled = list.some((d) => d.label);
  const parts = list.map((d) => km(d.m) + (labelled ? ' км' : '') + (d.label ? ` (${d.label})` : ''));
  return parts.join(' · ') + (labelled ? '' : ' км');
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
    note.textContent = '';
    try {
      if (fav) {
        await del('favorites/' + c.id);
        fav = false;
      } else {
        const { outcome } = await put('favorites/' + c.id);
        if (outcome === 'limit') {
          note.textContent = `В избранном уже ${m.limits.favorites} стартов — это предел. Уберите лишний, чтобы добавить новый.`;
        } else if (outcome === 'past') {
          note.textContent = 'Старт уже прошёл.';
        } else {
          fav = true;
        }
      }
    } catch (e) {
      note.textContent = errorText(e, 'Не удалось сохранить. Повторите позже.');
    }
    paint();
    btn.disabled = false;
  });
  paint();
  return [btn, note];
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
  const kind = (sport && sport.code !== 'running' ? sport.title + ' · ' : '') + c.type_label;

  const mark = el('p', { class: 'fav', text: '★ В избранном' });
  box.append(el('div', { class: 'card-head' }, plaque(c.sport, c.type, true),
    el('div', {}, el('h2', {}, statusBadge(c.status), statusBadge(c.status) ? ' ' : null, c.title),
      el('p', { class: 'hint', text: kind }), mark)));

  let when = ruDate(c.start_date);
  if (c.end_date && c.end_date !== c.start_date) when += ' – ' + ruDate(c.end_date);
  const rows = el('div', { class: 'rows' }, row('Дата', when));
  if (c.start_time) rows.append(row('Старт', `${c.start_time} (местное время)`));
  rows.append(row('Страна', [flag(c.country_code), c.country_name].filter(Boolean).join(' ')));
  rows.append(row('Место', [c.city || 'город не указан', c.address].filter(Boolean).join(', ')));
  if (c.distances.length) rows.append(row('Дистанции', distances(c.distances)));
  box.append(rows);

  if (c.description) box.append(el('p', { class: 'desc', text: c.description }));

  if (c.sources.length) {
    const links = [];
    c.sources.forEach((s, i) => {
      if (i) links.push(', ');
      links.push(s.url ? link(s.url, s.name) : s.name);
    });
    box.append(row(c.sources.length > 1 ? 'Источники' : 'Источник', ...links));
  }

  if (c.registration_url) {
    const a = link(c.registration_url, 'Регистрация');
    if (a.tagName === 'A') {
      a.className = 'btn btn-primary btn-block';
      box.append(a);
    }
  }
  box.append(...favControl(c, m, mark));
  box.append(el('a', { class: 'btn btn-block', href: '#/catalog', text: 'К каталогу' }));
}
