// Избранное: будущие избранные старты пользователя GET /favorites, кнопка
// «Ещё» подгружает следующую страницу; старт убирается прямо из списка.

import { del, get } from '../api.js';
import { el, errorText, flag, shortDate, showError, statusBadge } from '../ui.js';
import { plaque } from '../icons.js';

export const back = '#/';

export async function render(box) {
  box.replaceChildren();
  const title = el('h2', { text: 'Избранное' });
  const note = el('p', { class: 'hint', role: 'status' });
  const list = el('ul', { class: 'list' });
  const more = el('button', { type: 'button', class: 'btn btn-block', text: 'Ещё', hidden: true, onclick: () => load() });
  const foot = el('div', {});
  box.append(title, note, list, foot, more);

  // Упрощение: после удаления список перечитывается с первой страницы —
  // иначе сдвиг страниц пропустил бы старт; при длинных списках — удалять
  // строку на месте и догружать недостающую.
  async function remove(it, btn) {
    btn.disabled = true;
    try {
      await del('favorites/' + it.id);
    } catch (e) {
      if (!box.isConnected) return;
      note.textContent = errorText(e, 'Не удалось сохранить. Повторите позже.');
      btn.disabled = false;
      return;
    }
    if (box.isConnected) render(box);
  }

  function item(it) {
    const place = [shortDate(it.start_date), [flag(it.country_code), it.city].filter(Boolean).join(' ')].filter(Boolean).join(' · ');
    const btn = el('button', { type: 'button', class: 'btn', 'aria-label': 'Убрать из избранного', title: 'Убрать из избранного', text: '✕' });
    btn.addEventListener('click', () => remove(it, btn));
    return el('li', { class: 'fav-row' }, el('a', { class: 'item', href: '#/event/' + it.id },
      plaque(it.sport, ''),
      el('span', { class: 'body' },
        el('div', { class: 'meta', text: place }),
        el('div', { class: 'name' }, statusBadge(it.status), statusBadge(it.status) ? ' ' : null, it.title))), btn);
  }

  let page = 1;
  async function load() {
    more.disabled = true;
    try {
      const data = await get('favorites', { page });
      if (!box.isConnected) return;
      title.textContent = `Избранное: ${data.total}`;
      if (!data.total) {
        note.textContent = '';
        list.replaceChildren(el('li', { class: 'notice' },
          el('p', { text: 'Здесь пока пусто.' }),
          el('p', { class: 'hint', text: 'Откройте карточку старта в каталоге и нажмите «В избранное».' }),
          el('a', { class: 'btn btn-block', href: '#/catalog', text: 'К каталогу' })));
      }
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
