import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatMenuModule } from '@angular/material/menu';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Api } from '../../core/api.service';
import { Clock } from '../../core/clock.service';
import { Live } from '../../core/live.service';
import { Neighbor } from '../../core/models';
import { confirm } from '../../shared/confirm';
import { CountPipe, SincePipe } from '../../shared/format';
import { FsmStateBadge } from '../../shared/fsm-state';
import { Icon } from '../../shared/icon';
import { NeighborForm } from './neighbor-form';

@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [MatButtonModule, MatMenuModule, FsmStateBadge, Icon, CountPipe, SincePipe],
  template: `
    <div class="page">
      <div class="page-head">
        <div>
          <h1>Neighbors</h1>
          <p>IPv4 and IPv6 peers of this speaker. State and counters update live.</p>
        </div>
        <span class="spacer"></span>
        <button matButton="filled" (click)="edit(null)"><app-icon name="add" /> Add neighbor</button>
      </div>

      <section class="panel">
        @if (live.neighbors().length) {
          <div class="scroll-x">
            <table class="data">
              <thead>
                <tr>
                  <th>Peer</th>
                  <th class="num">Remote AS</th>
                  <th>State</th>
                  <th>Up / down for</th>
                  <th class="num">Received</th>
                  <th class="num">Sent</th>
                  <th>Negotiated capabilities</th>
                  <th><span class="sr">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                @for (n of live.neighbors(); track n.address) {
                  <tr>
                    <td>
                      <div class="mono">{{ n.address }}</div>
                      <div class="muted">{{ n.description || (n.remoteAsn === live.status()?.asn ? 'iBGP' : 'eBGP') }}</div>
                    </td>
                    <td class="num mono">{{ n.remoteAsn }}</td>
                    <td>
                      <app-fsm-state [state]="n.state" [adminDown]="!!n.adminDown" />
                      @if (n.state !== 'Established' && n.lastError) {
                        <div class="muted reason">{{ n.lastError }}</div>
                      }
                      @if (n.bfdState) {
                        <div class="muted reason">BFD {{ n.bfdState }}</div>
                      }
                    </td>
                    <td class="mono">{{ n.since | since: clock.now() }}</td>
                    <td class="num mono">
                      @for (f of families(n); track f) {
                        <div><span class="muted">{{ f }}</span> {{ n.counts[f]?.received | count }}</div>
                      }
                    </td>
                    <td class="num mono">
                      @for (f of families(n); track f) {
                        <div><span class="muted">{{ f }}</span> {{ n.counts[f]?.advertised | count }}</div>
                      }
                    </td>
                    <td class="caps">
                      @for (c of n.capabilities; track c) {
                        <span class="tag">{{ c }}</span>
                      } @empty {
                        <span class="muted">–</span>
                      }
                    </td>
                    <td class="num">
                      <button matIconButton [matMenuTriggerFor]="menu" [attr.aria-label]="'Actions for ' + n.address">
                        <app-icon name="more" />
                      </button>
                      <mat-menu #menu>
                        <button mat-menu-item (click)="edit(n)">Edit</button>
                        @if (n.adminDown) {
                          <button mat-menu-item (click)="act(n, 'enable', 'Enabled')">Enable</button>
                        } @else {
                          <button mat-menu-item (click)="shutdown(n)">Shut down</button>
                        }
                        <button mat-menu-item [disabled]="n.state !== 'Established'" (click)="soft(n, 'in')">
                          Soft reset inbound (route refresh)
                        </button>
                        <button mat-menu-item [disabled]="n.state !== 'Established'" (click)="soft(n, 'out')">
                          Soft reset outbound
                        </button>
                        <button mat-menu-item (click)="hardReset(n)">Hard reset</button>
                        <button mat-menu-item (click)="remove(n)">Delete</button>
                      </mat-menu>
                    </td>
                  </tr>
                }
              </tbody>
            </table>
          </div>
        } @else {
          <p class="empty">No neighbors yet. Add one to start a BGP session.</p>
        }
      </section>
    </div>
  `,
  styles: `
    .reason {
      font-size: 12px;
      white-space: normal;
      max-width: 240px;
    }
    .caps {
      white-space: normal !important;
      min-width: 220px;
      line-height: 1.9;
    }
    td {
      vertical-align: top;
    }
    .sr {
      position: absolute;
      clip-path: inset(50%);
    }
  `,
})
export default class NeighborsPage {
  protected readonly live = inject(Live);
  protected readonly clock = inject(Clock);
  private api = inject(Api);
  private dialog = inject(MatDialog);
  private snack = inject(MatSnackBar);

  protected families = (n: Neighbor) => (['ipv4', 'ipv6'] as const).filter((f) => n.counts[f]);

  protected edit(n: Neighbor | null) {
    const open = (data: unknown) =>
      this.dialog.open(NeighborForm, {
        data, panelClass: 'sheet', position: { right: '0', top: '0' }, height: '100vh', width: '760px', maxWidth: '100vw',
      });
    if (!n) return void open(null);
    // The live row carries state too; the form edits the stored configuration.
    this.api.config().subscribe((cfg) => open(cfg.neighbors.find((c) => c.address === n.address) ?? null));
  }

  protected act(n: Neighbor, action: string, done: string, body = {}) {
    this.api.neighborAction(n.address, action, body).subscribe(() => this.snack.open(`${done} ${n.address}`, '', { duration: 3000 }));
  }

  protected soft(n: Neighbor, direction: 'in' | 'out') {
    this.act(n, 'soft-reset', `Soft reset (${direction}bound) sent to`, { direction });
  }

  protected shutdown(n: Neighbor) {
    confirm(this.dialog, {
      title: `Shut down ${n.address}?`,
      message: 'The session closes with an administrative shutdown notification and stays down until you enable it.',
      action: 'Shut down',
      inputLabel: 'Message for the peer (optional)',
    }).subscribe((r) => this.act(n, 'disable', 'Shut down', { message: r.text }));
  }

  protected hardReset(n: Neighbor) {
    confirm(this.dialog, {
      title: `Hard reset ${n.address}?`,
      message: 'The session is torn down and re-established. All routes from this neighbor are withdrawn meanwhile.',
      action: 'Hard reset',
    }).subscribe(() => this.act(n, 'hard-reset', 'Hard reset'));
  }

  protected remove(n: Neighbor) {
    confirm(this.dialog, {
      title: `Delete ${n.address}?`,
      message: 'The session closes and the neighbor is removed from the configuration.',
      action: 'Delete neighbor',
    }).subscribe(() =>
      this.api.deleteNeighbor(n.address).subscribe(() => this.snack.open(`Deleted ${n.address}`, '', { duration: 3000 })),
    );
  }
}
