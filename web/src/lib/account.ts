import type { User } from './types';

// The server's rules, checked here first so mistakes show without a round trip.
export const EMAIL = /^[^\s@]+@[^\s@]+$/;
export const PASSWORD_MIN = 10; // bytes, as the server counts them
export const PASSWORD_MAX = 256;
export const NAME_MAX = 100;

const size = (s: string) => new TextEncoder().encode(s).length;

/** What is wrong with an email address, or '' when nothing is. */
export function emailProblem(email: string): string {
  if (!email) return 'Enter your email address.';
  if (!EMAIL.test(email) || email.length > 254) return "That email address doesn't look right.";
  return '';
}

/** What is wrong with a new password and its confirmation, or '' when nothing is. */
export function passwordProblem(password: string, confirm: string): string {
  if (size(password) < PASSWORD_MIN) return `Use at least ${PASSWORD_MIN} characters for the password.`;
  if (size(password) > PASSWORD_MAX) return `That password is too long: ${PASSWORD_MAX} characters at most.`;
  if (password !== confirm) return "The passwords don't match.";
  return '';
}

/** One or two letters for an avatar: from the name, else from the email. */
export function initials(u: Pick<User, 'name' | 'email'>): string {
  const words = u.name.trim().split(/\s+/).filter(Boolean);
  const parts = words.length ? words : u.email.split('@')[0].split(/[._+-]+/).filter(Boolean);
  if (!parts.length) return '?';
  const first = (w: string) => [...w][0] ?? '';
  const letters = parts.length > 1 ? first(parts[0]) + first(parts[parts.length - 1]) : first(parts[0]);
  return letters.toLocaleUpperCase();
}

export const roleLabel = (role: User['role']) => (role === 'admin' ? 'Admin' : 'User');
