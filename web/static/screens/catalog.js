// Каталог: чипы направлений, активные фильтры, страница выдачи GET /events по
// сохранённым фильтрам; кнопка «Ещё» подгружает следующую страницу.

import { get, meta } from '../api.js';
import { el, events, heading, showError } from '../ui.js';
import { glyph } from '../icons.js';
import { row } from '../row.js';
import { directions, filterTags, loadFilters, resetFilters, toQuery } from './filters.js';

export const back = '#/';

export async function render(box) {
  box.replaceChildren();
  let m;
  try {
    m = await meta();
  } catch (e) {
    if (box.isConnected) showError(box, e, () => render(box));
    return;
  }
  if (!box.isConnected) return;
  const f = loadFilters();
  const line = el('p', { class: 'hint', role: 'status' });
  const list = el('ul', { class: 'list list-2' });
  const more = el('button', { type: 'button', class: 'btn btn-block', text: 'Ещё', hidden: true, onclick: () => load() });
  const foot = el('div', {});
  const tags = el('div', {});

  let page = 1;
  let seq = 0;
  async function load() {
    const my = seq;
    more.disabled = true;
    try {
      const data = await get('events', { ...toQuery(f), page });
      if (my !== seq || !box.isConnected) return;
      line.textContent = `Найдено ${events(data.total)}`;
      if (page === 1 && !data.items.length) list.append(el('li', { class: 'hint', text: 'По этим фильтрам стартов пока нет.' }));
      for (const it of data.items) list.append(row(it, m));
      page = data.page + 1;
      more.hidden = data.page >= data.pages;
    } catch (e) {
      if (my === seq && box.isConnected) showError(foot, e, load);
    } finally {
      more.disabled = false;
    }
  }
  // reload — выдача с первой страницы после смены направления.
  function reload() {
    seq++;
    page = 1;
    more.hidden = true;
    list.replaceChildren();
    foot.replaceChildren();
    tags.replaceChildren(filterTags(f, m));
    load();
  }

  box.append(heading('Открой новое', 'Каталог стартов',
    el('a', { class: 'pill', href: '#/filters' }, glyph('sliders'), 'Фильтры'),
    el('button', { type: 'button', class: 'pill', text: 'Сбросить', onclick: () => { resetFilters(); render(box); } })),
  // Место поля текстового поиска (этап 6): input type="search" с подписью
  // «Название, город, страна…» добавляется сюда, перед чипами направлений,
  // а его значение — параметром запроса events в load(). Тогда же поле с
  // подписью «Город, соревнование или дистанция…» встаёт в герой главной
  // перед кнопкой «Найти старт» и ведёт в каталог с тем же значением.
  directions(m, f, reload), tags, line, list, foot, more);
  reload();
}
