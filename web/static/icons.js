// Встроенные SVG-значки: по виду спорта и дисциплине (D-06: вместо фото —
// значок на цветной плашке) и значки интерфейса. Контуры свои, простые,
// 24×24, рисуются обводкой currentColor; цвет и размер задаёт CSS.

const SVG_NS = 'http://www.w3.org/2000/svg';

// Контуры (атрибут d) по дисциплине; ключ '' — значок вида спорта по умолчанию.
const PATHS = {
  running: {
    // Шоссе: сходящиеся обочины и прерывистая осевая.
    road: ['M4 21 L10 3', 'M20 21 L14 3', 'M12 4v3', 'M12 11v3', 'M12 18v3'],
    // Трейл: горы.
    trail: ['M2 20 L9 7 L13 14 L16 10 L22 20 Z', 'M9 7 L11 10'],
    // Кросс: ель.
    cross: ['M12 3 L7 11 h3 L6 17 h12 L14 11 h3 Z', 'M12 17v4'],
    // Стадион: овал дорожки.
    track: ['M12 5 C18 5 21 8 21 12 C21 16 18 19 12 19 C6 19 3 16 3 12 C3 8 6 5 12 5 Z',
      'M12 9 C15 9 17 10.5 17 12 C17 13.5 15 15 12 15 C9 15 7 13.5 7 12 C7 10.5 9 9 12 9 Z'],
    // С препятствиями: барьер.
    obstacle: ['M5 21 V8 H19 V21', 'M5 13 H19', 'M3 21 H21'],
    // Прочее / вид спорта: финишный флаг.
    '': ['M6 21 V4', 'M6 4 H18 L15.5 8 L18 12 H6'],
  },
  skiing: {
    // Лыжные гонки: две лыжи и палки.
    ski: ['M3 17 L21 11', 'M3 21 L21 15', 'M8 4 L12 14', 'M16 3 L15 11'],
    // Лыжероллеры: лыжа с колёсами.
    rollerski: ['M4 14 L20 8', 'M4 18 L20 12', 'M6 20 m-1.5 0 a1.5 1.5 0 1 0 3 0 a1.5 1.5 0 1 0 -3 0',
      'M19 14 m-1.5 0 a1.5 1.5 0 1 0 3 0 a1.5 1.5 0 1 0 -3 0'],
    '': ['M3 17 L21 11', 'M3 21 L21 15', 'M8 4 L12 14', 'M16 3 L15 11'],
  },
};

// Значки интерфейса: шапка, нижняя панель, плитки, сердечко.
const GLYPHS = {
  pin: ['M12 21s-6-5.3-6-10a6 6 0 0 1 12 0c0 4.7-6 10-6 10z', 'M12 11m-2 0a2 2 0 1 0 4 0a2 2 0 1 0-4 0'],
  home: ['M3 11l9-7 9 7', 'M5 10v10h14V10'],
  grid: ['M4 4h7v7H4z', 'M13 4h7v7h-7z', 'M4 13h7v7H4z', 'M13 13h7v7h-7z'],
  calendar: ['M4 6h16v14H4z', 'M4 10h16', 'M8 3v4', 'M16 3v4'],
  heart: ['M12 20s-7-4.4-7-10a4 4 0 0 1 7-2.6A4 4 0 0 1 19 10c0 5.6-7 10-7 10z'],
  user: ['M12 8m-4 0a4 4 0 1 0 8 0a4 4 0 1 0-8 0', 'M4 21c0-4 3.6-6 8-6s8 2 8 6'],
  sliders: ['M4 7h10', 'M18 7h2', 'M4 17h2', 'M10 17h10', 'M16 7m-2 0a2 2 0 1 0 4 0a2 2 0 1 0-4 0',
    'M8 17m-2 0a2 2 0 1 0 4 0a2 2 0 1 0-4 0'],
  clock: ['M12 12m-9 0a9 9 0 1 0 18 0a9 9 0 1 0-18 0', 'M12 7v5l3 2'],
  chevron: ['M6 9l6 6 6-6'],
};

function svg(paths) {
  const node = document.createElementNS(SVG_NS, 'svg');
  node.setAttribute('viewBox', '0 0 24 24');
  node.setAttribute('aria-hidden', 'true');
  node.setAttribute('class', 'ico');
  for (const d of paths) {
    const p = document.createElementNS(SVG_NS, 'path');
    p.setAttribute('d', d);
    node.append(p);
  }
  return node;
}

// icon — <svg> значка для вида спорта sport и дисциплины type; неизвестные
// значения получают значок бега.
export function icon(sport, type) {
  const bySport = PATHS[sport] || PATHS.running;
  return svg(bySport[type] || bySport['']);
}

// glyph — <svg> значка интерфейса по имени.
export function glyph(name) {
  return svg(GLYPHS[name]);
}

// plaque — значок на плашке; класс плашки зависит от вида спорта.
export function plaque(sport, type, large) {
  const box = document.createElement('span');
  box.className = 'plaque' + (sport === 'skiing' ? ' plaque-skiing' : '') + (large ? ' plaque-lg' : '');
  box.append(icon(sport, type));
  return box;
}
