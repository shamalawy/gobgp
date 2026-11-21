import { Pipe, PipeTransform } from '@angular/core';

const numberFormat = new Intl.NumberFormat('en-US');

/** 1234567 -> "1,234,567" */
@Pipe({ name: 'count' })
export class CountPipe implements PipeTransform {
  transform(v: number | null | undefined): string {
    return v == null ? '–' : numberFormat.format(Math.round(v));
  }
}

/** Bytes as KiB/MiB/GiB. */
@Pipe({ name: 'bytes' })
export class BytesPipe implements PipeTransform {
  transform(v: number | null | undefined): string {
    if (v == null) return '–';
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
    let i = 0;
    for (; v >= 1024 && i < units.length - 1; i++) v /= 1024;
    return `${v.toFixed(i === 0 || v >= 100 ? 0 : 1)} ${units[i]}`;
  }
}

/** Time elapsed since a unix timestamp in seconds, as "3d 04:12:09". */
@Pipe({ name: 'since' })
export class SincePipe implements PipeTransform {
  transform(unix: number | null | undefined, now: number): string {
    if (!unix) return '–';
    const s = Math.max(0, Math.floor(now / 1000 - unix));
    const pad = (n: number) => String(n).padStart(2, '0');
    const clock = `${pad(Math.floor(s / 3600) % 24)}:${pad(Math.floor(s / 60) % 60)}:${pad(s % 60)}`;
    return s >= 86400 ? `${Math.floor(s / 86400)}d ${clock}` : clock;
  }
}
