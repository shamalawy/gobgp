import { AbstractControl, ValidationErrors, ValidatorFn } from '@angular/forms';

const IPV4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/;

export function isIPv4(s: string): boolean {
  return IPV4.test(s);
}

/** RFC 4291 text forms, including "::" compression and an embedded IPv4 tail. */
export function isIPv6(s: string): boolean {
  if (!s.includes(':') || s.includes(':::')) return false;
  const lastColon = s.lastIndexOf(':');
  if (s.slice(lastColon + 1).includes('.')) {
    if (!isIPv4(s.slice(lastColon + 1))) return false;
    s = s.slice(0, lastColon + 1) + '0:0'; // the IPv4 tail stands for two groups
  }
  const halves = s.split('::');
  if (halves.length > 2) return false;
  const groups = halves.flatMap((h) => (h === '' ? [] : h.split(':')));
  if (!groups.every((g) => /^[0-9a-fA-F]{1,4}$/.test(g))) return false;
  return halves.length === 2 ? groups.length < 8 : groups.length === 8;
}

export const isIP = (s: string) => isIPv4(s) || isIPv6(s);

export function isCidr(s: string): boolean {
  const [addr, len, extra] = s.split('/');
  if (extra !== undefined || !/^\d{1,3}$/.test(len ?? '')) return false;
  return (isIPv4(addr) && +len <= 32) || (isIPv6(addr) && +len <= 128);
}

/** Builds a validator that passes empty values; pair it with Validators.required. */
function check(key: string, ok: (value: string) => boolean): ValidatorFn {
  return (c: AbstractControl): ValidationErrors | null => {
    const v = String(c.value ?? '').trim();
    return v === '' || ok(v) ? null : { [key]: true };
  };
}

const isAsn = (v: string) => /^\d{1,10}$/.test(v) && +v >= 1 && +v <= 4294967295;
const each = (v: string, ok: (item: string) => boolean) => v.split(/[\s,]+/).filter(Boolean).every(ok);
const u16 = (v: string) => /^\d{1,5}$/.test(v) && +v <= 65535;
const u32 = (v: string) => /^\d{1,10}$/.test(v) && +v <= 4294967295;

export const ipAddress = check('ip', isIP);
export const ipv4Address = check('ipv4', isIPv4);
export const cidr = check('cidr', isCidr);
export const ipOrCidr = check('ipOrCidr', (v) => isIP(v) || isCidr(v));
/** A 4-octet AS number (RFC 6793): 1 to 4294967295. */
export const asn = check('asn', isAsn);
export const asPath = check('asPath', (v) => each(v, isAsn));
export const communities = check('communities', (v) =>
  each(v, (c) => /^[a-z-]+$/.test(c) || (c.split(':').length === 2 && c.split(':').every(u16))),
);
export const largeCommunities = check('largeCommunities', (v) =>
  each(v, (c) => c.split(':').length === 3 && c.split(':').every(u32)),
);
export const extCommunities = check('extCommunities', (v) => each(v, (c) => /^(rt|soo):[^:\s]+:\d+$/i.test(c)));
export const regex = check('regex', (v) => {
  try {
    new RegExp(v);
    return true;
  } catch {
    return false;
  }
});

/** The message for the first failing validator of a control. */
export function errorText(c: AbstractControl | null): string {
  const e = c?.errors;
  if (!e) return '';
  if (e['required']) return 'Required';
  if (e['min']) return `At least ${e['min'].min}`;
  if (e['max']) return `At most ${e['max'].max}`;
  if (e['ip']) return 'Enter an IPv4 or IPv6 address';
  if (e['ipv4']) return 'Enter an IPv4 address';
  if (e['cidr']) return 'Enter a prefix such as 192.0.2.0/24 or 2001:db8::/32';
  if (e['ipOrCidr']) return 'Enter an address or a prefix';
  if (e['asn']) return 'Enter an AS number from 1 to 4294967295';
  if (e['asPath']) return 'Enter AS numbers separated by spaces';
  if (e['communities']) return 'Use the form 65000:100, separated by spaces';
  if (e['largeCommunities']) return 'Use the form 65000:1:2, separated by spaces';
  if (e['extCommunities']) return 'Use the form rt:65000:100 or soo:65000:100';
  if (e['regex']) return 'Not a valid regular expression';
  return 'Invalid value';
}

/** Splits a space or comma separated field into its items. */
export function words(v: string | null | undefined): string[] {
  return (v ?? '').split(/[\s,]+/).filter(Boolean);
}
