import { Injectable, signal } from '@angular/core';

/** The current time in milliseconds, updated every second, for live durations. */
@Injectable({ providedIn: 'root' })
export class Clock {
  readonly now = signal(Date.now());

  constructor() {
    setInterval(() => this.now.set(Date.now()), 1000);
  }
}
