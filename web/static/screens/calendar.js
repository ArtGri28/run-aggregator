// Календарь: сетка месяца с подсветкой дней со стартами (GET /calendar) и под
// ней список «Старты месяца» (GET /events?from=&to=). Нажатие на день
// оставляет в списке только его старты, повторное — возвращает месяц. Месяц
// и день — в hash: #/calendar/ГГГГ-ММ и #/calendar/ГГГГ-ММ-ДД. Действуют
// фильтры каталога, кроме периодов дат: даты здесь задаёт сам календарь.

import { get, meta } from '../api.js';
import { el, events, heading, plural, ruDate, showError } from '../ui.js';
import { row } from '../row.js';
import { filterTags, loadFilters, toQuery } from './filters.js';

const MONTHS = ['Январь', 'Февраль', 'Март', 'Апрель', 'Май', 'Июнь',
  'Июль', 'Август', 'Сентябрь', 'Октябрь', 'Ноябрь', 'Декабрь'];
const WEEKDAYS = ['Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб', 'Вс'];

// back — из дня в его месяц, из месяца на главную.
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
  let day = param && param.length === 10 && validDay(param) && param >= today ? param : '';
  let ym = (param || '').slice(0, 7);
  if (!validDay(ym + '-01') || ym < today.slice(0, 7)) ym = today.slice(0, 7);

  const [y, mo] = ym.split('-').map(Number);
  const shift = (k) => {
    const d = new Date(Date.UTC(y, mo - 1 + k, 1));
    return d.getUTCFullYear() + '-' + pad(d.getUTCMonth() + 1);
  };
  const last = new Date(Date.UTC(y, mo, 0)).getUTCDate();
  // Понедельник — первый день недели.
  const lead = (new Date(Date.UTC(y, mo - 1, 1)).getUTCDay() + 6) % 7;
  const isCurrent = ym === today.slice(0, 7);
  const first = isCurrent ? today : ym + '-01';
  const end = `${ym}-${pad(last)}`;
  const name = `${MONTHS[mo - 1]} ${y}`;

  const prev = isCurrent
    ? el('button', { type: 'button', class: 'btn', disabled: true, 'aria-label': 'Предыдущий месяц', text: '‹' })
    : el('a', { class: 'btn', href: '#/calendar/' + shift(-1), 'aria-label': 'Предыдущий месяц', text: '‹' });
  const next = el('a', { class: 'btn', href: '#/calendar/' + shift(1), 'aria-label': 'Следующий месяц', text: '›' });
  const grid = el('div', { class: 'cal' });
  const calFoot = el('div', {});
  const title = el('h2', {});
  const whole = el('button', { type: 'button', class: 'pill', text: 'Весь месяц', onclick: () => select('') });
  const line = el('p', { class: 'hint', role: 'status' });
  const list = el('ul', { class: 'list list-2' });
  const more = el('button', { type: 'button', class: 'btn btn-block', text: 'Ещё', hidden: true, onclick: () => load() });
  const foot = el('div', {});
  box.append(heading('Планируй сезон', 'Календарь', el('span', { class: 'pill', text: name })),
    filterTags(f, m),
    el('div', { class: 'card' }, el('div', { class: 'cal-nav' }, prev, el('h2', { text: name }), next), grid, calFoot),
    el('div', { class: 'bar' }, title, whole), line, list, foot, more);

  let counts = {};
  function draw() {
    grid.replaceChildren(...WEEKDAYS.map((w) => el('div', { class: 'cal-wd', text: w })));
    for (let i = 0; i < lead; i++) grid.append(el('div', {}));
    for (let d = 1; d <= last; d++) {
      const iso = `${ym}-${pad(d)}`;
      const n = counts[iso] || 0;
      const attrs = {
        class: 'cal-day' + (iso === today ? ' today' : '') + (iso < today ? ' past' : '') + (n ? ' has' : '') + (iso === day ? ' sel' : ''),
        'aria-current': iso === today ? 'date' : null,
        text: String(d),
      };
      // Кнопка — только у дня со стартами; прошедшие дни сервер не отдаёт.
      grid.append(n
        ? el('button', {
          ...attrs, type: 'button', 'aria-pressed': String(iso === day),
          'aria-label': `${ruDate(iso)}: ${n} ${plural(n, 'старт', 'старта', 'стартов')}`,
          onclick: () => select(iso === day ? '' : iso),
        })
        : el('div', attrs));
    }
  }

  let page = 1;
  let seq = 0;
  async function load() {
    const my = seq;
    more.disabled = true;
    try {
      const data = await get('events', { ...toQuery(f), from: day || first, to: day || end, page });
      if (my !== seq || !box.isConnected) return;
      line.textContent = data.total ? `Найдено ${events(data.total)}` : (day ? 'В этот день стартов нет.' : 'В этом месяце стартов нет.');
      for (const it of data.items) list.append(row(it, m));
      page = data.page + 1;
      more.hidden = data.page >= data.pages;
    } catch (e) {
      if (my === seq && box.isConnected) showError(foot, e, load);
    } finally {
      more.disabled = false;
    }
  }

  // select — список дня d или всего месяца (''); hash меняется без перехода,
  // чтобы «Назад» и карточка возвращали в то же место.
  function select(d) {
    day = d;
    window.history.replaceState(null, '', '#/calendar/' + (day || ym));
    seq++;
    page = 1;
    // Год — в пилюле заголовка: «Старты 18 октября».
    title.textContent = day ? `Старты ${ruDate(day).slice(0, -5)}` : 'Старты месяца';
    whole.hidden = !day;
    line.textContent = '';
    more.hidden = true;
    list.replaceChildren();
    foot.replaceChildren();
    draw();
    load();
  }

  async function loadCounts() {
    try {
      const days = await get('calendar', { ...toQuery(f), from: first, to: end });
      if (!box.isConnected) return;
      counts = Object.fromEntries(days.map((d) => [d.date, d.count]));
      draw();
    } catch (e) {
      if (box.isConnected) showError(calFoot, e, loadCounts);
    }
  }
  select(day);
  loadCounts();
}
