# Log Stream API

## Endpoint

```text
GET /api/logs/stream
```

The endpoint keeps the HTTP connection open and writes one NDJSON line per
event.

```text
Content-Type: application/x-ndjson; charset=utf-8
```

## Stream Behavior

- The default stream emits connection events only.
- Each event is a single JSON object followed by `\n`.
- The server flushes after every event.
- The connection remains open until the client disconnects or a write fails.
- A heartbeat is sent every 20 seconds while no log events are written.
- At most 5 stream clients can be connected at the same time.

## Query Parameters

```text
include=all
```

Emit all sing-box logs. Without this parameter, only connection logs are
emitted.

```text
sinceTime=2026-05-06T15:30:12%2B08:00
```

Replay historical events after this time before switching to live streaming.
The `+` in timezone offsets must be URL encoded as `%2B`.

```text
sinceId=evt-1778052612000000000
```

Replay historical events after this stream event ID. Use either `sinceId` or
`sinceTime`, not both.

```text
limit=1000
```

Maximum number of historical log entries inspected for replay. The maximum is
5000.

## Event Examples

Connection event:

```json
{"type":"connection","id":"evt-1778052612000000000","time":"2026-05-06T15:30:12+08:00","level":"info","message":"INFO outbound/trojan[台湾 05]: outbound connection to 104.18.32.47:443","source":"sing-box","component":"outbound/trojan","tag":"台湾 05","direction":"outbound","action":"outbound connection to","endpoint":"104.18.32.47:443"}
```

Heartbeat:

```json
{"type":"heartbeat","time":"2026-05-06T15:30:30+08:00"}
```

## Error Responses

- `400`: invalid `sinceId` or `sinceTime`.
- `405`: method is not `GET`.
- `429`: too many concurrent stream clients.
- `500`: HTTP streaming is not supported by the response writer.

## Test Cases

Read live connection events:

```bash
curl -N -H 'Accept: application/x-ndjson' \
  'http://127.0.0.1:3333/api/logs/stream'
```

Read all live logs, including DNS and health logs:

```bash
curl -N -H 'Accept: application/x-ndjson' \
  'http://127.0.0.1:3333/api/logs/stream?include=all'
```

Replay recent history and continue streaming:

```bash
curl -N -H 'Accept: application/x-ndjson' \
  'http://127.0.0.1:3333/api/logs/stream?sinceTime=2026-05-06T15:30:12%2B08:00&limit=500'
```

Validate line-by-line JSON parsing:

```bash
curl -N 'http://127.0.0.1:3333/api/logs/stream?include=all' \
  | while IFS= read -r line; do echo "$line" | jq -c .; done
```
