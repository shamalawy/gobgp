import { Injectable, effect, signal } from '@angular/core';

export type Scheme = 'light' | 'dark';

/** Light or dark scheme: the stored choice, or else the system preference. */
@Injectable({ providedIn: 'root' })
export class Theme {
  readonly scheme = signal<Scheme>(
    (localStorage.getItem('scheme') as Scheme | null) ??
      (matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'),
  );

  constructor() {
    effect(() => (document.documentElement.dataset['scheme'] = this.scheme()));
  }

  toggle() {
    this.scheme.update((s) => (s === 'dark' ? 'light' : 'dark'));
    localStorage.setItem('scheme', this.scheme());
  }
}
