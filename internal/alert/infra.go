package alert

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Ключи инфраструктурных алертов (MON-03, MON-04, D-12, D-13).
const (
	KeyWorkerStale     = "worker_stale"
	KeyBotStale        = "bot_stale"
	KeyBackupStale     = "backup_stale"
	KeyBackupCopyStale = "backup_copy_stale"
	KeyPostgresDown    = "postgres_down"
)

const (
	// serviceMaxAge — отметка bot или worker старше — сервис не работает.
	serviceMaxAge = 10 * time.Minute
	// backupMaxAge — отметка копии старше — ночной запуск пропущен (D-13).
	backupMaxAge = 26 * time.Hour
	// dbFailsToAlert — столько опросов БД подряд без ответа до postgres_down.
	dbFailsToAlert = 3
	// agesTimeout — предел одного опроса отметок.
	agesTimeout = 10 * time.Second
)

// keyText — что случилось, по ключу алерта.
var keyText = map[string]string{
	KeyWorkerStale:     "worker не отмечается: сбор событий, вероятно, остановлен",
	KeyBotStale:        "bot не отмечается",
	KeyBackupStale:     "локальная резервная копия не создавалась",
	KeyBackupCopyStale: "внешняя резервная копия не отправлялась",
	KeyPostgresDown:    "PostgreSQL не отвечает",
}

// Watch — отметка, за которой следит цикл: старше MaxAge или отсутствует —
// плохое состояние ключа Key.
type Watch struct {
	Key, Component string
	MaxAge         time.Duration
}

// BotWatch — за чем следит bot: worker и две отметки резервных копий
// (локальная и внешняя — независимо, D-12).
func BotWatch() []Watch {
	return []Watch{
		{KeyWorkerStale, "worker", serviceMaxAge},
		{KeyBackupStale, "backup", backupMaxAge},
		{KeyBackupCopyStale, "backup_copy", backupMaxAge},
	}
}

// WorkerWatch — за чем следит worker: отметка bot.
func WorkerWatch() []Watch {
	return []Watch{{KeyBotStale, "bot", serviceMaxAge}}
}

// infraState — состояние одного ключа в памяти процесса.
type infraState struct {
	// notified — плохое состояние доведено до администратора (или записано
	// в лог в режиме «только лог»).
	notified   bool
	remindedAt time.Time
	// fails — неудачных отправок подряд; nextTry — не раньше этого времени
	// следующая попытка.
	fails   int
	nextTry time.Time
}

// InfraLoop следит за отметками heartbeats и доступностью БД и сообщает
// администраторам о смене состояния: один алерт, напоминание раз в 24 часа,
// «Восстановлено» при возврате. Состояние — в памяти: после перезапуска
// процесса неустранённый алерт уходит повторно один раз.
type InfraLoop struct {
	// Ages — возраст отметок по компонентам; ошибка — БД недоступна.
	Ages  func(ctx context.Context) (map[string]time.Duration, error)
	Watch []Watch
	// DBKey — ключ алерта о недоступной БД; "" — недоступность только
	// подавляет остальные ключи.
	DBKey  string
	Admins []int64
	// Send — как Loop.Send; nil или пустой Admins — режим «только лог».
	Send func(ctx context.Context, chatID int64, text string, acceptRunID int64) error
	// Now — часы (nil → time.Now); Log (nil → slog.Default()); Every — период
	// опроса (0 → минута).
	Now   func() time.Time
	Log   *slog.Logger
	Every time.Duration

	states  map[string]*infraState
	dbFails int
}

// infraDecide — что делать с ключом: bad — состояние сейчас плохое, st —
// его запись (nil — записи нет).
func infraDecide(bad bool, st *infraState, now time.Time) action {
	switch {
	case st != nil && now.Before(st.nextTry):
		return none
	case !bad:
		if st != nil && st.notified {
			return reset
		}
		return none
	case st == nil || !st.notified:
		return send
	case now.Sub(st.remindedAt) >= remindEvery:
		return remind
	}
	return none
}

// backoff — пауза перед следующей попыткой отправки после fails неудач
// подряд: 1, 2, 4, 8, 15, 15… минут (MON-04).
func backoff(fails int) time.Duration {
	if fails < 1 {
		return 0
	}
	if fails > 4 {
		return 15 * time.Minute
	}
	return time.Minute << (fails - 1)
}

func (l *InfraLoop) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *InfraLoop) log() *slog.Logger {
	if l.Log != nil {
		return l.Log
	}
	return slog.Default()
}

// span — возраст для текста: минуты до двух часов, дальше часы.
func span(d time.Duration) string {
	if d < 2*time.Hour {
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	}
	return fmt.Sprintf("%d ч", int(d.Hours()))
}

// Tick один раз сверяет отметки и БД с состоянием ключей. Пока БД не
// отвечает, остальные ключи не вычисляются и их состояние не меняется.
func (l *InfraLoop) Tick(ctx context.Context) {
	actx, cancel := context.WithTimeout(ctx, agesTimeout)
	ages, err := l.Ages(actx)
	cancel()
	if ctx.Err() != nil {
		return
	}
	now := l.now()
	if err != nil {
		l.dbFails++
		if l.DBKey != "" && l.dbFails >= dbFailsToAlert {
			l.apply(ctx, l.DBKey, true, now, fmt.Sprintf("%s (%s): %d опроса подряд без ответа", keyText[l.DBKey], l.DBKey, dbFailsToAlert))
		}
		return
	}
	l.dbFails = 0
	if l.DBKey != "" {
		l.apply(ctx, l.DBKey, false, now, "")
	}
	for _, w := range l.Watch {
		age, ok := ages[w.Component]
		detail := "отметки нет"
		if ok {
			detail = fmt.Sprintf("последняя отметка %s назад, порог %s", span(age), span(w.MaxAge))
		}
		l.apply(ctx, w.Key, !ok || age > w.MaxAge, now, fmt.Sprintf("%s (%s): %s", keyText[w.Key], w.Key, detail))
	}
}

// apply выполняет действие по ключу. Состояние «оповещён» ставится только
// после доставки хотя бы одному администратору; неудача откладывает
// следующую попытку по ряду backoff.
func (l *InfraLoop) apply(ctx context.Context, key string, bad bool, now time.Time, text string) {
	st := l.states[key]
	if !bad && st != nil && !st.notified {
		// Вернулось в норму до первой доставки: сообщать не о чем.
		delete(l.states, key)
		return
	}
	act := infraDecide(bad, st, now)
	switch act {
	case none:
		return
	case remind:
		text = "Напоминание: " + text
	case reset:
		text = "Восстановлено: " + key
	}
	if st == nil {
		if l.states == nil {
			l.states = map[string]*infraState{}
		}
		st = &infraState{}
		l.states[key] = st
	}
	if !l.deliver(ctx, key, text, st.fails) {
		st.fails++
		st.nextTry = now.Add(backoff(st.fails))
		return
	}
	if act == reset {
		delete(l.states, key)
		return
	}
	st.notified, st.remindedAt, st.fails, st.nextTry = true, now, 0, time.Time{}
}

// deliver шлёт текст каждому администратору; true, если дошло хотя бы до
// одного. Без получателей или Send — запись в лог, считается доставкой.
// В лог — ключ и тип ошибки без её текста: WARN при первой неудаче подряд,
// INFO при доставке после неудач.
func (l *InfraLoop) deliver(ctx context.Context, key, text string, fails int) bool {
	if l.Send == nil || len(l.Admins) == 0 {
		l.log().Warn("infra alert", "key", key, "text", text)
		return true
	}
	ok := false
	var errType string
	for _, id := range l.Admins {
		if err := l.Send(ctx, id, text, 0); err != nil {
			errType = fmt.Sprintf("%T", err)
			continue
		}
		ok = true
	}
	switch {
	case !ok && fails == 0:
		l.log().Warn("infra alert send failed", "key", key, "err_type", errType)
	case ok && fails > 0:
		l.log().Info("infra alert delivered", "key", key, "after_failures", fails)
	}
	return ok
}

// Run делает Tick раз в Every до отмены ctx. Первый тик — через Every, а не
// сразу: при одновременном старте сервисов отметка соседа появляется в
// первые секунды.
func (l *InfraLoop) Run(ctx context.Context) {
	every := l.Every
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.Tick(ctx)
		}
	}
}
