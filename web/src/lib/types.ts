export interface AppConfig {
  app_name: string;
  version: string;
  public_url: string;
  max_requests: number;
  max_body_size: number;
  max_delay: number;
  token_ttl: number;
  /** Seconds a URL's audit record outlives it; 0 = none kept. */
  retention: number;
  abuse_contact: string;
  /** User accounts are on (ACCOUNTS=on). */
  accounts: boolean;
  /** Anyone may create URLs without signing in. Always true with accounts off. */
  anonymous_urls: boolean;
}

export type Role = 'admin' | 'user';

export interface User {
  id: string;
  email: string;
  name: string;
  role: Role;
  created_at: string;
  last_login_at: string | null;
}

/** What a one-time link is for: the first admin, an invitation or a password reset. */
export type LinkKind = 'setup' | 'invite' | 'reset';

export interface LinkInfo {
  kind: LinkKind;
  /** The invited or reset address; empty for setup. */
  email: string;
  role: Role;
}

export interface RedeemInput {
  token: string;
  email?: string; // setup only
  name?: string; // setup and invite
  password: string;
}

/** An API key as listed; the key itself is sent once, on creation. */
export interface ApiKey {
  id: string;
  name: string;
  /** The first characters after `wgo_`, to tell keys apart. */
  prefix: string;
  created_at: string;
  last_used_at: string | null;
}

export interface NewApiKey extends ApiKey {
  /** The full key, shown this once. */
  key: string;
}

export interface KV {
  name: string;
  value: string;
}

export type SignatureScheme = 'github' | 'stripe' | 'standard' | 'shopify' | 'slack' | 'malga' | 'hmac';
export type SignatureStatus = 'valid' | 'invalid' | 'missing' | 'expired' | 'unverifiable';
export type HmacAlgorithm = 'sha1' | 'sha256' | 'sha512';
export type HmacEncoding = 'hex' | 'base64';

/** How a URL checks signatures. The secret is never sent back. */
export interface SignatureSettings {
  scheme: SignatureScheme;
  header?: string;
  algorithm?: HmacAlgorithm;
  encoding?: HmacEncoding;
  prefix?: string;
  tolerance: number;
  reject: boolean;
  has_secret: boolean;
}

export interface SignatureInput extends Partial<Omit<SignatureSettings, 'has_secret'>> {
  /** Empty keeps the saved secret. */
  secret?: string;
}

export interface SignatureResult {
  scheme: SignatureScheme;
  status: SignatureStatus;
  detail?: string;
}

export interface Token {
  uuid: string;
  description: string;
  default_status: number;
  default_content: string;
  default_content_type: string;
  response_headers: KV[];
  timeout: number;
  cors: boolean;
  signature?: SignatureSettings;
  ip: string;
  user_agent: string;
  created_at: string;
  updated_at: string;
  url: string;
  request_count: number;
  /** null for private URLs, which don't expire. */
  expires_at: string | null;
  /** The account a private URL belongs to; absent on anonymous ones. */
  owner_id?: string;
  /** Saves an anonymous URL to an account later. Sent once, by create and release, with accounts on. */
  claim_key?: string;
}

/** One of the signed-in user's URLs (GET /token). */
export interface TokenItem extends Token {
  /** When the newest stored request arrived. */
  latest_request_at: string | null;
}

export type TokenInput = Partial<
  Pick<
    Token,
    | 'default_status'
    | 'default_content'
    | 'default_content_type'
    | 'timeout'
    | 'cors'
    | 'description'
    | 'response_headers'
  >
> & { signature?: SignatureInput | null };

export interface UploadedFile {
  field: string;
  filename: string;
  content_type: string;
  size: number;
}

/** Lightweight request used by the list and live events. */
export interface Summary {
  uuid: string;
  method: string;
  path: string;
  query_string: string;
  ip: string;
  content_type: string;
  size: number;
  response_status: number;
  signature?: SignatureStatus;
  created_at: string;
}

export interface FullRequest {
  uuid: string;
  token_id: string;
  method: string;
  url: string;
  path: string;
  hostname: string;
  ip: string;
  user_agent: string;
  protocol: string;
  headers: Record<string, string[]>;
  query: KV[];
  form: KV[];
  files: UploadedFile[];
  content_type: string;
  content: string;
  encoding?: 'base64';
  size: number;
  truncated: boolean;
  response_status: number;
  signature?: SignatureResult;
  created_at: string;
}

export interface Page<T> {
  data: T[];
  total: number;
  per_page: number;
  current_page: number;
  is_last_page: boolean;
  from: number;
  to: number;
}

export type LiveEvent =
  | { type: 'connected'; total: number }
  | { type: 'resync'; total: number }
  | { type: 'request.created'; request: Summary; total: number; evicted?: string[] }
  | { type: 'request.deleted'; id: string; total: number }
  | { type: 'requests.cleared'; total: number }
  | { type: 'token.updated'; token: Token; total: number }
  | { type: 'token.deleted'; total: number }
  // Who may see the URL changed. No token data: anonymous viewers may be listening.
  | { type: 'token.claimed' }
  | { type: 'token.released' };
