export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB'];
  let i = -1;
  do {
    n /= 1024;
    i++;
  } while (n >= 1024 && i < units.length - 1);
  return `${n < 10 ? n.toFixed(1) : Math.round(n)} ${units[i]}`;
}

/** "12s", "3m", "2h", "4d" */
export function shortAgo(iso: string, now: number): string {
  const s = Math.max(0, Math.round((now - Date.parse(iso)) / 1000));
  if (s < 60) return s < 5 ? 'now' : `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });

export function ago(iso: string, now: number): string {
  const s = Math.round((Date.parse(iso) - now) / 1000);
  const abs = Math.abs(s);
  if (abs < 5) return 'just now';
  if (abs < 60) return rtf.format(s, 'second');
  if (abs < 3600) return rtf.format(Math.round(s / 60), 'minute');
  if (abs < 86400) return rtf.format(Math.round(s / 3600), 'hour');
  return rtf.format(Math.round(s / 86400), 'day');
}

// Times show in the browser's time zone, or in UTC when `utc` is set.
const formats = (timeZone?: string) => ({
  // timeStyle long names the zone: "Oct 21, 2026, 6:05:50 PM GMT-3"
  dateTime: new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'long', timeZone }),
  clock: new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23', timeZone }),
});
const local = formats();
const utcFormats = formats('UTC');

export const timeZone = local.dateTime.resolvedOptions().timeZone;
export const dateTime = (iso: string, utc = false) => (utc ? utcFormats : local).dateTime.format(new Date(iso));
/** "18:05:50", or "21:05:50 UTC" */
export const clock = (iso: string, utc = false) => (utc ? `${utcFormats.clock.format(new Date(iso))} UTC` : local.clock.format(new Date(iso)));

export function duration(seconds: number): string {
  if (seconds % 86400 === 0) return `${seconds / 86400} day${seconds === 86400 ? '' : 's'}`;
  if (seconds % 3600 === 0) return `${seconds / 3600} hour${seconds === 3600 ? '' : 's'}`;
  return `${Math.round(seconds / 60)} minutes`;
}

const KNOWN = ['get', 'post', 'put', 'patch', 'delete'];
export const methodColor = (m: string) => `var(--m-${KNOWN.includes(m.toLowerCase()) ? m.toLowerCase() : 'other'})`;

export const statusClass = (code: number) => `s${String(code)[0]}`;

const STATUS: Record<number, string> = {
  200: 'OK', 201: 'Created', 202: 'Accepted', 204: 'No Content', 301: 'Moved Permanently', 302: 'Found',
  304: 'Not Modified', 307: 'Temporary Redirect', 308: 'Permanent Redirect', 400: 'Bad Request',
  401: 'Unauthorized', 403: 'Forbidden', 404: 'Not Found', 405: 'Method Not Allowed', 408: 'Request Timeout',
  409: 'Conflict', 410: 'Gone', 413: 'Payload Too Large', 415: 'Unsupported Media Type', 418: "I'm a teapot",
  422: 'Unprocessable Content', 429: 'Too Many Requests', 500: 'Internal Server Error', 501: 'Not Implemented',
  502: 'Bad Gateway', 503: 'Service Unavailable', 504: 'Gateway Timeout',
};
export const statusText = (code: number) => STATUS[code] ?? '';
export const COMMON_STATUSES = Object.keys(STATUS).map(Number);

/** "content-type" -> "Content-Type" */
export const headerCase = (name: string) => name.replace(/(^|-)([a-z])/g, (_, d: string, c: string) => d + c.toUpperCase());

export const shortId = (id: string) => id.slice(0, 8);
