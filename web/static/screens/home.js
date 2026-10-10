// Главная: герой, чипы направлений, плитки разделов и три ближайших старта
// по текущим фильтрам.

import { get, meta } from '../api.js';
import { el, showError } from '../ui.js';
import { glyph } from '../icons.js';
import { row } from '../row.js';
import { directions, loadFilters, toQuery } from './filters.js';

export const back = null;

function tile(name, title, sub, href) {
  return el('a', { class: 'tile', href }, glyph(name), el('span', { class: 'title', text: title }), el('span', { class: 'sub', text: sub }));
}

export async function render(box) {
  // Поле текстового поиска героя появится вместе с поиском каталога — см.
  // пометку в screens/catalog.js; до тех пор здесь только кнопка.
  box.replaceChildren(el('section', { class: 'hero' },
    el('p', { class: 'eyebrow', text: 'Каталог спортивных событий' }),
    el('h1', { text: 'Найди свой следующий старт' }),
    el('p', { class: 'sub', text: 'Старты России и Беларуси — от городских забегов до трейлов' }),
    el('div', { class: 'find' }, el('a', { class: 'btn btn-primary', href: '#/catalog', text: 'Найти старт ↗' }))));
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
      const data = await get('events', { ...toQuery(f), limit: 3 });
      if (my !== seq || !box.isConnected) return;
      list.replaceChildren(...data.items.map((it) => row(it, m)));
      if (!data.items.length) list.append(el('li', { class: 'hint', text: 'По этим фильтрам стартов пока нет.' }));
    } catch (e) {
      if (my === seq && box.isConnected) showError(foot, e, load);
    }
  }

  box.append(el('h2', { text: 'Выбери направление' }), directions(m, f, load),
    el('div', { class: 'tiles' },
      tile('clock', 'Ближайшие старты', 'По дате', '#/catalog'),
      tile('calendar', 'Календарь', 'Планируй сезон', '#/calendar'),
      tile('heart', 'Избранное', 'Твои события', '#/favorites'),
      tile('sliders', 'Фильтры', 'Настрой поиск', '#/filters')),
    el('div', { class: 'bar' }, el('h2', { text: 'Ближайшие старты' }), el('a', { href: '#/catalog', text: 'Весь каталог →' })),
    list, foot);
  load();
}
