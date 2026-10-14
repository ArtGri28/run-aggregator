// Главная: шапка с полем поиска, круглые кнопки видов спорта и пять
// ближайших стартов по текущим фильтрам.

import { get, meta } from '../api.js';
import { el, showError } from '../ui.js';
import { glyph } from '../icons.js';
import { row } from '../row.js';
import { directions, loadFilters, toQuery } from './filters.js';
import { catalogHash } from './catalog.js';

export const back = null;

export async function render(box) {
  // Поле шапки ведёт в каталог с набранным запросом; пустое — просто в каталог.
  const query = el('input', {
    type: 'search', placeholder: 'Название или город…', 'aria-label': 'Поиск стартов',
    enterkeyhint: 'search', autocomplete: 'off', maxlength: '64',
  });
  const find = (e) => {
    e.preventDefault();
    window.location.hash = catalogHash(query.value);
  };
  box.replaceChildren(el('section', { class: 'hero' },
    el('p', { class: 'eyebrow', text: 'Каталог спортивных событий' }),
    el('h1', { text: 'Найди свой следующий старт' }),
    el('p', { class: 'sub', text: 'Старты России и Беларуси — от городских забегов до трейлов' }),
    el('form', { class: 'search', role: 'search', onsubmit: find }, el('div', { class: 'box' }, glyph('search'), query),
      el('button', { type: 'submit', class: 'btn btn-primary', text: 'Найти' }))));
  let m;
  try {
    m = await meta();
  } catch (e) {
    if (box.isConnected) showError(box, e, () => render(box));
    return;
  }
  if (!box.isConnected) return;
  const f = loadFilters();
  const list = el('ul', { class: 'list' });
  const foot = el('div', {});

  let seq = 0;
  async function load() {
    const my = ++seq;
    foot.replaceChildren();
    try {
      const data = await get('events', { ...toQuery(f), limit: 5 });
      if (my !== seq || !box.isConnected) return;
      list.replaceChildren(...data.items.map((it) => row(it, m)));
      if (!data.items.length) list.append(el('li', { class: 'notice' }, el('p', { text: 'По этим фильтрам стартов пока нет.' })));
    } catch (e) {
      if (my === seq && box.isConnected) showError(foot, e, load);
    }
  }

  box.append(directions(m, f, load, true),
    el('div', { class: 'bar' }, el('h2', { text: 'Ближайшие старты' }), el('a', { href: '#/catalog', text: 'Весь каталог →' })),
    list, foot);
  load();
}
