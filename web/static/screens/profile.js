// Профиль: ответы анкеты GET /profile только для чтения. Анкета заполняется
// и меняется в боте; кнопка закрывает Mini App и возвращает в чат.

import { get } from '../api.js';
import { el, showError } from '../ui.js';

export const back = '#/';

function row(key, value) {
  return el('div', { class: 'row' }, el('span', { class: 'k', text: key }), el('span', { class: 'v', text: value }));
}

function toBot(text) {
  return el('button', { type: 'button', class: 'btn btn-block', text, onclick: () => window.Telegram.WebApp.close() });
}

export async function render(box) {
  box.replaceChildren(el('h2', { text: 'Профиль' }));
  let p;
  try {
    p = await get('profile');
  } catch (e) {
    if (box.isConnected) showError(box, e, () => render(box));
    return;
  }
  if (!box.isConnected) return;
  if (!p.exists) {
    box.append(el('div', { class: 'notice' },
      el('p', { text: 'Анкета заполняется в боте.' }),
      el('p', { class: 'hint', text: 'Вернитесь в чат с ботом и отправьте команду /start.' })),
    toBot('Вернуться в бот'));
    return;
  }
  const label = (o, empty) => (o ? o.label : empty);
  const home = p.country ? [p.country.label, p.city].filter(Boolean).join(', ') : 'не указано';
  box.append(el('div', { class: 'rows' },
    row('Пол', label(p.gender, 'не указан')),
    row('Возраст', label(p.age_group, 'не указан')),
    row('Живу', home),
    row('Спорт', p.sports.length ? p.sports.map((s) => s.label).join(', ') : 'не указан')),
  el('p', { class: 'hint', text: 'Ответы анкеты меняются в боте, в разделе «Мой профиль».' }),
  toBot('Изменить в боте'));
}
