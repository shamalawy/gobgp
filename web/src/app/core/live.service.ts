import { Injectable, computed, signal } from '@angular/core';
import { FsmEvent, InjectStatus, Status } from './models';

const LOG_LIMIT = 200;
const RATE_SAMPLES = 120; // one minute of injection throughput at two samples a second

/**
 * Live daemon state, fed by the server-sent event stream. The browser
 * reconnects the stream by itself; `connected` reflects whether it is up.
 */
@Injectable({ providedIn: 'root' })
export class Live {
  readonly connected = signal(false);
  readonly status = signal<Status | null>(null);
  readonly log = signal<FsmEvent[]>([]); // newest first
  readonly inject = signal<InjectStatus | null>(null);
  readonly rates = signal<number[]>([]); // routes per second while an injection runs

  readonly neighbors = computed(() => this.status()?.neighbors ?? []);

  constructor() {
    const source = new EventSource('/api/v1/events');
    source.onopen = () => {
      this.connected.set(true);
      this.log.set([]); // the server replays its backlog on every connect
    };
    source.onerror = () => this.connected.set(false);
    source.addEventListener('status', (e) => this.status.set(JSON.parse(e.data)));
    source.addEventListener('log', (e) => this.log.update((l) => [JSON.parse(e.data), ...l].slice(0, LOG_LIMIT)));
    source.addEventListener('inject', (e) => {
      const st: InjectStatus = JSON.parse(e.data);
      if (st.state === 'running') {
        const fresh = this.inject()?.started !== st.started;
        this.rates.update((r) => [...(fresh ? [] : r), st.rate].slice(-RATE_SAMPLES));
      }
      this.inject.set(st);
    });
  }
}
