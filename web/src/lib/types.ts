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
  expires_at: string;
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
  | { type: 'token.deleted'; total: number };
