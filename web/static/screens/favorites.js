// Избранное: будущие избранные старты пользователя GET /favorites, кнопка
// «Ещё» подгружает следующую страницу; сердечко убирает старт из списка.

import { get, meta } from '../api.js';
import { el, events, heading, showError } from '../ui.js';
import { row } from '../row.js';

export const back = '#/';

export async function render(box) {
  box.replaceChildren();
  const count = el('span', { class: 'pill', role: 'status', hidden: true });
  const list = el('ul', { class: 'list list-2' });
  const more = el('button', { type: 'button', class: 'btn btn-block', text: 'Ещё', hidden: true, onclick: () => load() });
  const foot = el('div', {});
  box.append(heading('Сохрани на потом', 'Избранное', count), list, foot, more);

  let page = 1;
  async function load() {
    more.disabled = true;
    try {
      const [m, data] = await Promise.all([meta(), get('favorites', { page })]);
      if (!box.isConnected) return;
      count.textContent = events(data.total);
      count.hidden = false;
      if (!data.total) {
        list.replaceChildren(el('li', { class: 'notice' },
          el('p', { text: 'Здесь пока пусто.' }),
          el('p', { class: 'hint', text: 'Нажмите на сердечко у старта в каталоге — он появится здесь.' }),
          el('a', { class: 'btn btn-block', href: '#/catalog', text: 'К каталогу' })));
      }
      // Упрощение: после удаления список перечитывается с первой страницы —
      // иначе сдвиг страниц пропустил бы старт; при длинных списках — удалять
      // строку на месте и догружать недостающую.
      for (const it of data.items) list.append(row(it, m, { fav: true, onChange: () => { if (box.isConnected) render(box); } }));
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
