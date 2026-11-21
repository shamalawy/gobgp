import { DatePipe } from '@angular/common';
import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { RouterLink } from '@angular/router';
import { Api } from '../../core/api.service';
import { Clock } from '../../core/clock.service';
import { Live } from '../../core/live.service';
import { BytesPipe, CountPipe, SincePipe } from '../../shared/format';
import { FsmStateBadge } from '../../shared/fsm-state';
import { Icon } from '../../shared/icon';
import { SpeakerSettings } from './speaker-settings';

@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [DatePipe, RouterLink, MatButtonModule, FsmStateBadge, Icon, CountPipe, BytesPipe, SincePipe],
  template: `
    <div class="page">
      <div class="page-head">
        <h1>Dashboard</h1>
        <span class="spacer"></span>
        <button matButton="outlined" (click)="editSpeaker()"><app-icon name="settings" /> Speaker settings</button>
      </div>

      @if (live.status(); as st) {
        <section class="panel speaker">
          <dl>
            <div><dt>Local AS</dt><dd class="mono">{{ st.asn }}</dd></div>
            <div><dt>Router ID</dt><dd class="mono">{{ st.routerId }}</dd></div>
            <div><dt>Listen port</dt><dd class="mono">{{ st.listenPort < 0 ? 'off' : st.listenPort }}</dd></div>
            <div><dt>Running for</dt><dd class="mono">{{ st.started | since: clock.now() }}</dd></div>
            <div>
              <dt>Sessions established</dt>
              <dd class="mono">{{ st.established }} of {{ st.neighbors.length }}</dd>
            </div>
          </dl>
        </section>

        <div class="grid">
          <section class="panel">
            <div class="panel-head">
              <h2>Sessions</h2>
              <span class="spacer"></span>
              <a routerLink="/neighbors">Manage neighbors</a>
            </div>
            @if (st.neighbors.length) {
              <div class="scroll-x">
                <table class="data">
                  <thead>
                    <tr>
                      <th>Neighbor</th><th>State</th><th>Since</th>
                      <th class="num">Received</th><th class="num">Sent</th>
                    </tr>
                  </thead>
                  <tbody>
                    @for (n of st.neighbors; track n.address) {
                      <tr>
                        <td>
                          <span class="mono">{{ n.address }}</span>
                          <span class="muted"> AS{{ n.remoteAsn }} {{ n.description }}</span>
                        </td>
                        <td><app-fsm-state [state]="n.state" [adminDown]="!!n.adminDown" /></td>
                        <td class="mono">{{ n.since | since: clock.now() }}</td>
                        <td class="num mono">{{ received(n) | count }}</td>
                        <td class="num mono">{{ advertised(n) | count }}</td>
                      </tr>
                    }
                  </tbody>
                </table>
              </div>
            } @else {
              <p class="empty">No neighbors yet. <a routerLink="/neighbors">Add one</a> to start peering.</p>
            }
          </section>

          <section class="panel">
            <div class="panel-head"><h2>Routes</h2></div>
            <table class="data">
              <thead>
                <tr><th>Table</th><th class="num">IPv4</th><th class="num">IPv6</th></tr>
              </thead>
              <tbody>
                <tr>
                  <td>Adj-RIB-In <span class="muted">received from neighbors</span></td>
                  <td class="num mono">{{ st.ribs.ipv4.adjIn | count }}</td>
                  <td class="num mono">{{ st.ribs.ipv6.adjIn | count }}</td>
                </tr>
                <tr>
                  <td>Loc-RIB <span class="muted">paths</span></td>
                  <td class="num mono">{{ st.ribs.ipv4.locRib | count }}</td>
                  <td class="num mono">{{ st.ribs.ipv6.locRib | count }}</td>
                </tr>
                <tr>
                  <td>Loc-RIB <span class="muted">prefixes</span></td>
                  <td class="num mono">{{ st.ribs.ipv4.prefixes | count }}</td>
                  <td class="num mono">{{ st.ribs.ipv6.prefixes | count }}</td>
                </tr>
                <tr>
                  <td>Adj-RIB-Out <span class="muted">sent to neighbors</span></td>
                  <td class="num mono">{{ st.ribs.ipv4.adjOut | count }}</td>
                  <td class="num mono">{{ st.ribs.ipv6.adjOut | count }}</td>
                </tr>
              </tbody>
            </table>
          </section>

          <section class="panel">
            <div class="panel-head">
              <h2>Session events</h2>
              <span class="muted">newest first</span>
            </div>
            @if (live.log().length) {
              <ol class="log" aria-live="polite">
                @for (e of live.log(); track $index) {
                  <li>
                    <time class="mono muted">{{ e.time | date: 'HH:mm:ss.SSS' }}</time>
                    <span class="mono">{{ e.peer }}</span>
                    <app-fsm-state [state]="e.state" />
                    <span class="muted">{{ e.reason }}</span>
                  </li>
                }
              </ol>
            } @else {
              <p class="empty">State changes of neighbor sessions appear here as they happen.</p>
            }
          </section>

          <section class="panel">
            <div class="panel-head"><h2>Daemon process</h2></div>
            <table class="data">
              <tbody>
                <tr>
                  <td>CPU <span class="muted">of {{ st.runtime.cpus }} cores</span></td>
                  <td class="num mono">{{ st.runtime.cpuPercent.toFixed(1) }} %</td>
                </tr>
                <tr><td>Heap in use</td><td class="num mono">{{ st.runtime.heapAlloc | bytes }}</td></tr>
                <tr><td>Memory from the OS</td><td class="num mono">{{ st.runtime.sys | bytes }}</td></tr>
                <tr><td>Goroutines</td><td class="num mono">{{ st.runtime.goroutines | count }}</td></tr>
                <tr><td>GC cycles</td><td class="num mono">{{ st.runtime.gcCycles | count }}</td></tr>
              </tbody>
            </table>
          </section>
        </div>
      } @else {
        <p class="panel empty">Waiting for the daemon…</p>
      }
    </div>
  `,
  styles: `
    .speaker dl {
      display: flex;
      flex-wrap: wrap;
      margin: 0;
    }
    .speaker div {
      padding: 14px 20px;
      border-right: 1px solid var(--line);
      flex: 1 1 150px;
    }
    .speaker div:last-child {
      border-right: 0;
    }
    dt {
      color: var(--muted);
      font-size: 12px;
    }
    dd {
      margin: 2px 0 0;
      font-size: 20px;
      font-weight: 500;
    }
    .grid {
      display: grid;
      grid-template-columns: minmax(0, 1.7fr) minmax(300px, 1fr);
      gap: 20px;
      align-items: start;
    }
    .log {
      list-style: none;
      margin: 0;
      padding: 0;
      max-height: 340px;
      overflow-y: auto;
    }
    .log li {
      display: flex;
      flex-wrap: wrap;
      gap: 4px 14px;
      align-items: center;
      padding: 6px 16px;
      border-bottom: 1px solid var(--line);
    }
    .log li:last-child {
      border-bottom: 0;
    }
    @media (max-width: 1000px) {
      .grid {
        grid-template-columns: minmax(0, 1fr);
      }
    }
  `,
})
export default class DashboardPage {
  protected readonly live = inject(Live);
  protected readonly clock = inject(Clock);
  private api = inject(Api);
  private dialog = inject(MatDialog);

  protected received = (n: { counts: Record<string, { received: number } | undefined> }) =>
    (n.counts['ipv4']?.received ?? 0) + (n.counts['ipv6']?.received ?? 0);
  protected advertised = (n: { counts: Record<string, { advertised: number } | undefined> }) =>
    (n.counts['ipv4']?.advertised ?? 0) + (n.counts['ipv6']?.advertised ?? 0);

  protected editSpeaker() {
    this.api.config().subscribe((cfg) => this.dialog.open(SpeakerSettings, { data: cfg.global, width: '640px' }));
  }
}
