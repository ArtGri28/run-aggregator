// Встроенные SVG-значки по виду спорта и дисциплине (D-06: вместо фото —
// значок на цветной плашке). Контуры свои, простые, 24×24, рисуются
// обводкой currentColor; цвет и размер задаёт CSS плашки.

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

// icon — <svg> значка для вида спорта sport и дисциплины type; неизвестные
// значения получают значок бега.
export function icon(sport, type) {
  const bySport = PATHS[sport] || PATHS.running;
  const paths = bySport[type] || bySport[''];
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('aria-hidden', 'true');
  for (const d of paths) {
    const p = document.createElementNS(SVG_NS, 'path');
    p.setAttribute('d', d);
    svg.append(p);
  }
  return svg;
}

// plaque — значок на плашке; класс плашки зависит от вида спорта.
export function plaque(sport, type, large) {
  const box = document.createElement('span');
  box.className = 'plaque' + (sport === 'skiing' ? ' plaque-skiing' : '') + (large ? ' plaque-lg' : '');
  box.append(icon(sport, type));
  return box;
}
