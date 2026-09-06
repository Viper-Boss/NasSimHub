// Wires the Bark push notifier into the communication service.
import fs from 'node:fs'

const file = 'internal/communication/service.go'
let s = fs.readFileSync(file, 'utf8').replace(/\r\n/g, '\n')

let changed = false

if (!s.includes('internal/barkpush')) {
  s = s.replace(
    '"github.com/human-agent65535/modemdeck/internal/agentclient"',
    '"github.com/human-agent65535/modemdeck/internal/agentclient"\n\t"github.com/human-agent65535/modemdeck/internal/barkpush"'
  )
  changed = true
}

if (!s.includes('bark                    *barkpush.Notifier')) {
  s = s.replace(
    '\tevents                  messageevents.Publisher',
    '\tevents                  messageevents.Publisher\n\tbark                    *barkpush.Notifier'
  )
  changed = true
}

if (!s.includes('func (s *Service) SetBarkNotifier')) {
  s = s.replace(
    'func (s *Service) publishIncomingMessages(messages []store.Message, observedAt time.Time) {',
    `// SetBarkNotifier attaches the optional Bark push notifier for incoming
// SMS (nil disables push).
func (s *Service) SetBarkNotifier(notifier *barkpush.Notifier) {
	s.bark = notifier
}

func (s *Service) publishIncomingMessages(messages []store.Message, observedAt time.Time) {`
  )
  changed = true
}

const hookAfter = `\t\t\tTimestamp:  message.Timestamp,
\t\t\tObservedAt: observedAt,
\t\t})
\t}
}`
const hookNew = `\t\t\tTimestamp:  message.Timestamp,
\t\t\tObservedAt: observedAt,
\t\t})
\t}
\tif s.bark != nil {
\t\tfor _, message := range messages {
\t\t\tnotify := message
\t\t\tgo func() {
\t\t\t\tctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
\t\t\t\tdefer cancel()
\t\t\t\ts.bark.NotifyIncomingSMS(ctx, barkpush.MessageInfo{
\t\t\t\t\tMessageID: strconv.FormatInt(notify.ID, 10),
\t\t\t\t\tPeer:      notify.Peer,
\t\t\t\t\tContent:   notify.Content,
\t\t\t\t})
\t\t\t}()
\t\t}
\t}
}`
if (!s.includes(hookAfter)) {
  console.log('hook anchor MISS')
  process.exit(1)
}
s = s.replace(hookAfter, hookNew)
changed = true

fs.writeFileSync(file, s)
console.log('communication service hooked, changed=' + changed)
