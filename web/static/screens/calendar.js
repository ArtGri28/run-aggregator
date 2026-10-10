// Календарь: сетка месяца с числом стартов по дням (GET /calendar) и список
// стартов дня (GET /events?from=день&to=день). Месяц и день — в hash:
// #/calendar/ГГГГ-ММ и #/calendar/ГГГГ-ММ-ДД. Действуют фильтры каталога,
// кроме периодов дат: даты здесь задаёт сам календарь.

import { get, meta } from '../api.js';
import { el, plural, ruDate, showError } from '../ui.js';
import { item } from './catalog.js';
import { loadFilters, summary, toQuery } from './filters.js';

const MONTHS = ['Январь', 'Февраль', 'Март', 'Апрель', 'Май', 'Июнь',
  'Июль', 'Август', 'Сентябрь', 'Октябрь', 'Ноябрь', 'Декабрь'];
const WEEKDAYS = ['Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб', 'Вс'];

// back — из списка дня в его месяц, из месяца на главную.
export const back = (param) => (param && param.length === 10 ? '#/calendar/' + param.slice(0, 7) : '#/');

const pad = (n) => String(n).padStart(2, '0');

// validDay — существующая дата YYYY-MM-DD.
function validDay(iso) {
  const d = new Date(iso + 'T00:00:00Z');
  return !Number.isNaN(d.getTime()) && d.toISOString().slice(0, 10) === iso;
}

// Упрощение: «сегодня» — из meta (по Москве, как у сервера), а meta
// запрашивается один раз за сеанс: в сеансе через полночь выделен вчерашний
// день. При необходимости — перечитывать meta при смене даты.
export async function render(box, param) {
  box.replaceChildren();
  let m;
  try {
    m = await meta();
  } catch (e) {
    if (box.isConnected) showError(box, e, () => render(box, param));
    return;
  }
  if (!box.isConnected) return;
  const f = { ...loadFilters(), periods: [] };
  const today = m.today;
  // Прошедший или несуществующий день и месяц раньше текущего — текущий месяц.
  if (param && param.length === 10 && validDay(param) && param >= today) {
    renderDay(box, param, f);
    return;
  }
  let ym = (param || '').slice(0, 7);
  if (!validDay(ym + '-01') || ym < today.slice(0, 7)) ym = today.slice(0, 7);
  renderMonth(box, ym, today, f, m);
}

function renderMonth(box, ym, today, f, m) {
  const [y, mo] = ym.split('-').map(Number);
  const shift = (k) => {
    const d = new Date(Date.UTC(y, mo - 1 + k, 1));
    return d.getUTCFullYear() + '-' + pad(d.getUTCMonth() + 1);
  };
  const last = new Date(Date.UTC(y, mo, 0)).getUTCDate();
  // Понедельник — первый день недели.
  const lead = (new Date(Date.UTC(y, mo - 1, 1)).getUTCDay() + 6) % 7;
  const isCurrent = ym === today.slice(0, 7);

  const prev = isCurrent
    ? el('button', { type: 'button', class: 'btn', disabled: true, 'aria-label': 'Предыдущий месяц', text: '‹' })
    : el('a', { class: 'btn', href: '#/calendar/' + shift(-1), 'aria-label': 'Предыдущий месяц', text: '‹' });
  const next = el('a', { class: 'btn', href: '#/calendar/' + shift(1), 'aria-label': 'Следующий месяц', text: '›' });
  const grid = el('div', { class: 'cal' });
  const note = el('p', { class: 'hint', role: 'status' });
  const foot = el('div', {});
  box.append(el('div', { class: 'bar' }, prev, el('h2', { text: `${MONTHS[mo - 1]} ${y}` }), next),
    el('p', { class: 'hint', text: summary(f, m) }), grid, note, foot);

  function draw(counts) {
    grid.replaceChildren(...WEEKDAYS.map((w) => el('div', { class: 'cal-wd', text: w })));
    for (let i = 0; i < lead; i++) grid.append(el('div', {}));
    for (let d = 1; d <= last; d++) {
      const iso = `${ym}-${pad(d)}`;
      const n = counts[iso] || 0;
      const attrs = {
        class: 'cal-day' + (iso === today ? ' today' : '') + (iso < today ? ' past' : '') + (n ? ' has' : ''),
        'aria-current': iso === today ? 'date' : null,
      };
      const num = el('span', { class: 'num', text: String(d) });
      // Ссылка — только у дня со стартами; прошедшие дни сервер не отдаёт.
      grid.append(n
        ? el('a', { ...attrs, href: '#/calendar/' + iso, 'aria-label': `${ruDate(iso)}: ${n} ${plural(n, 'старт', 'старта', 'стартов')}` },
          num, el('span', { class: 'cnt', text: String(n) }))
        : el('div', attrs, num));
    }
  }

  async function load() {
    try {
      const days = await get('calendar', { ...toQuery(f), from: isCurrent ? today : ym + '-01', to: `${ym}-${pad(last)}` });
      if (!box.isConnected) return;
      const counts = {};
      let total = 0;
      for (const d of days) {
        counts[d.date] = d.count;
        total += d.count;
      }
      draw(counts);
      note.textContent = total ? `Найдено: ${total}` : 'В этом месяце стартов нет.';
    } catch (e) {
      if (box.isConnected) showError(foot, e, load);
    }
  }
  draw({});
  load();
}

function renderDay(box, day, f) {
  const line = el('p', { class: 'hint' });
  const list = el('ul', { class: 'list' });
  const more = el('button', { type: 'button', class: 'btn btn-block', text: 'Ещё', hidden: true, onclick: () => load() });
  const foot = el('div', {});
  box.append(el('h2', { text: ruDate(day) }), line, list, foot, more);

  let page = 1;
  async function load() {
    more.disabled = true;
    try {
      const data = await get('events', { ...toQuery(f), from: day, to: day, page });
      if (!box.isConnected) return;
      line.textContent = `Найдено: ${data.total}`;
      if (page === 1 && !data.items.length) list.append(el('li', { class: 'hint', text: 'В этот день стартов нет.' }));
      for (const it of data.items) list.append(item(it));
      page = data.page + 1;
      more.hidden = data.page >= data.pages;
    } catch (e) {
      if (box.isConnected) showError(foot, e, load);
    } finally {
      more.disabled = false;
    }
  }
  load();
}
