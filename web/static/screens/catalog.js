// Каталог: страница выдачи GET /events по сохранённым фильтрам, кнопка «Ещё»
// подгружает следующую страницу. Состав строки — как у кнопки списка бота:
// дата, флаг и город, название; плюс значок вида спорта и дистанции.

import { get, meta } from '../api.js';
import { el, flag, kmList, shortDate, showError, statusBadge } from '../ui.js';
import { plaque } from '../icons.js';
import { loadFilters, summary, toQuery } from './filters.js';

export const back = '#/';

// item — строка списка стартов; её же показывает список дня календаря.
export function item(it) {
  const place = [shortDate(it.start_date), [flag(it.country_code), it.city].filter(Boolean).join(' ')].filter(Boolean).join(' · ');
  return el('li', {}, el('a', { class: 'item', href: '#/event/' + it.id },
    plaque(it.sport, ''),
    el('span', { class: 'body' },
      el('div', { class: 'meta', text: place }),
      el('div', { class: 'name' }, statusBadge(it.status), statusBadge(it.status) ? ' ' : null, it.title),
      it.distances.length ? el('div', { class: 'dist', text: kmList(it.distances) }) : null)));
}

export async function render(box) {
  box.replaceChildren();
  const f = loadFilters();
  const title = el('h2', { text: 'Каталог' });
  const line = el('p', { class: 'hint' });
  const list = el('ul', { class: 'list' });
  const more = el('button', { type: 'button', class: 'btn btn-block', text: 'Ещё', hidden: true, onclick: () => load() });
  const foot = el('div', {});
  box.append(el('div', { class: 'bar' }, title, el('a', { class: 'btn', href: '#/filters', text: 'Фильтры' })), line, list, foot, more);

  let page = 1;
  async function load() {
    more.disabled = true;
    try {
      const [m, data] = await Promise.all([meta(), get('events', { ...toQuery(f), page })]);
      if (!box.isConnected) return;
      title.textContent = `Найдено: ${data.total}`;
      line.textContent = summary(f, m) || 'Фильтры не заданы';
      if (page === 1 && !data.items.length) list.append(el('li', { class: 'hint', text: 'По этим фильтрам стартов пока нет.' }));
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
