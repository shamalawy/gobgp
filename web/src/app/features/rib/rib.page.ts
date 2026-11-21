import { CdkVirtualScrollViewport, ScrollingModule } from '@angular/cdk/scrolling';
import { CollectionViewer, DataSource } from '@angular/cdk/collections';
import { DatePipe } from '@angular/common';
import { ChangeDetectionStrategy, Component, inject, signal, viewChild } from '@angular/core';
import { NonNullableFormBuilder, ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { BehaviorSubject, Observable, Subscription } from 'rxjs';
import { Api } from '../../core/api.service';
import { Clock } from '../../core/clock.service';
import { Live } from '../../core/live.service';
import { RibPage, RibQuery, RibRow } from '../../core/models';
import { CountPipe, SincePipe } from '../../shared/format';
import { Icon } from '../../shared/icon';
import { errorText, ipAddress, ipOrCidr, regex } from '../../shared/validators';

const PAGE = 200;
const MAX_CACHED_PAGES = 10; // 2,000 rows in the browser however long the table is

/**
 * Feeds the virtual scroll viewport from the server-side paged RIB. Only the
 * pages around the viewport are held; the rest of the million rows stay on
 * the server.
 */
class RibDataSource extends DataSource<RibRow | undefined> {
  private rows: (RibRow | undefined)[] = [];
  private stream = new BehaviorSubject<(RibRow | undefined)[]>([]);
  private pages = new Map<number, boolean>(); // page -> loaded; iteration order is least recently used first
  private requests = new Subscription();
  private inFlight = 0;

  constructor(
    private api: Api,
    private query: RibQuery,
    private refresh: boolean, // whether the first request makes the daemon re-read the table
    private loaded: (page: RibPage) => void,
    private busy: (loading: boolean) => void,
  ) {
    super();
    this.fetch(0);
  }

  connect(viewer: CollectionViewer): Observable<(RibRow | undefined)[]> {
    this.requests.add(
      viewer.viewChange.subscribe((range) => {
        for (let p = Math.floor(range.start / PAGE); p <= Math.floor(Math.max(range.end - 1, 0) / PAGE); p++) this.fetch(p);
      }),
    );
    return this.stream;
  }

  disconnect() {
    this.requests.unsubscribe();
  }

  private fetch(page: number) {
    const known = this.pages.get(page);
    this.pages.delete(page);
    this.pages.set(page, known ?? false); // now the most recently used
    if (known !== undefined) return;

    this.busy(++this.inFlight > 0);
    const refresh = this.refresh;
    this.refresh = false;
    const request = this.api.rib(this.query, page * PAGE, PAGE, refresh).subscribe({
      next: (res) => {
        if (this.rows.length !== res.total) this.rows = Array.from({ length: res.total });
        this.rows.splice(page * PAGE, res.rows.length, ...res.rows);
        this.pages.set(page, true);
        for (const [old, done] of this.pages) {
          if (this.pages.size <= MAX_CACHED_PAGES) break;
          if (done && old !== page) {
            this.pages.delete(old);
            this.rows.fill(undefined, old * PAGE, (old + 1) * PAGE);
          }
        }
        this.stream.next(this.rows);
        this.loaded(res);
      },
      error: () => this.pages.delete(page),
    });
    request.add(() => this.busy(--this.inFlight > 0));
    this.requests.add(request);
  }
}

@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    DatePipe, ReactiveFormsModule, ScrollingModule, MatButtonModule, MatFormFieldModule, MatInputModule,
    MatProgressBarModule, MatSelectModule, Icon, CountPipe, SincePipe,
  ],
  template: `
    <div class="page">
      <div class="page-head">
        <div>
          <h1>RIB explorer</h1>
          <p>
            Browse a snapshot of the routing tables, served page by page. With an RPKI cache configured, the Loc-RIB
            shows best paths only.
          </p>
        </div>
      </div>

      <form class="panel filters" [formGroup]="form" (ngSubmit)="search()">
        <div class="fields">
          <mat-form-field>
            <mat-label>Table</mat-label>
            <mat-select formControlName="table">
              <mat-option value="global">Loc-RIB</mat-option>
              <mat-option value="adj-in">Adj-RIB-In (received)</mat-option>
              <mat-option value="adj-out">Adj-RIB-Out (sent)</mat-option>
            </mat-select>
          </mat-form-field>
          @if (form.value.table !== 'global') {
            <mat-form-field>
              <mat-label>Neighbor</mat-label>
              <mat-select formControlName="neighbor">
                @for (n of live.neighbors(); track n.address) {
                  <mat-option [value]="n.address">{{ n.address }} (AS{{ n.remoteAsn }})</mat-option>
                }
              </mat-select>
            </mat-form-field>
          }
          <mat-form-field>
            <mat-label>Address family</mat-label>
            <mat-select formControlName="family">
              <mat-option value="ipv4">IPv4 unicast</mat-option>
              <mat-option value="ipv6">IPv6 unicast</mat-option>
            </mat-select>
          </mat-form-field>
          <mat-form-field>
            <mat-label>Prefix or address</mat-label>
            <input matInput formControlName="prefix" class="mono" placeholder="203.0.113.0/24" />
            <mat-error>{{ err('prefix') }}</mat-error>
          </mat-form-field>
          <mat-form-field>
            <mat-label>Prefix match</mat-label>
            <mat-select formControlName="match">
              <mat-option value="">Automatic</mat-option>
              <mat-option value="exact">Exact</mat-option>
              <mat-option value="longest">Longest match</mat-option>
              <mat-option value="longer">This prefix and more specifics</mat-option>
              <mat-option value="shorter">This prefix and covering routes</mat-option>
            </mat-select>
            <mat-hint>Automatic: longest match for an address</mat-hint>
          </mat-form-field>
          <mat-form-field>
            <mat-label>Next hop</mat-label>
            <input matInput formControlName="nextHop" class="mono" />
            <mat-error>{{ err('nextHop') }}</mat-error>
          </mat-form-field>
          <mat-form-field>
            <mat-label>AS path (regular expression)</mat-label>
            <input matInput formControlName="asPath" class="mono" placeholder="^65001 | 3356$" />
            <mat-error>{{ err('asPath') }}</mat-error>
          </mat-form-field>
          <mat-form-field>
            <mat-label>Community</mat-label>
            <input matInput formControlName="community" class="mono" placeholder="65000:100" />
          </mat-form-field>
          <mat-form-field>
            <mat-label>Paths</mat-label>
            <mat-select formControlName="best">
              <mat-option value="">All paths</mat-option>
              <mat-option value="true">Best paths only</mat-option>
              <mat-option value="false">Non-best paths only</mat-option>
            </mat-select>
          </mat-form-field>
        </div>
        <div class="actions">
          <button matButton="filled">Search</button>
          <button matButton type="button" (click)="reset()">Clear filters</button>
          <button matButton type="button" (click)="search(true)"><app-icon name="refresh" /> Reload table</button>
          <span class="muted summary" role="status">
            @if (page(); as p) {
              {{ p.total | count }} of {{ p.size | count }} {{ p.bestOnly ? 'best paths' : 'paths' }}, read
              {{ p.snapshot | date: 'HH:mm:ss' }}
            } @else if (loading()) {
              Reading the table…
            }
          </span>
        </div>
      </form>

      <section class="panel table">
        <mat-progress-bar [mode]="loading() ? 'indeterminate' : 'determinate'" value="0" />
        <div class="row head" role="row">
          <span></span><span>Prefix</span><span>Next hop</span><span>AS path</span><span class="num">MED</span>
          <span class="num">Local pref</span><span>Origin</span><span>RPKI</span><span>Communities</span><span>From</span>
          <span>Age</span>
        </div>
        @let ds = source();
        <cdk-virtual-scroll-viewport itemSize="34" minBufferPx="400" maxBufferPx="800">
            <div class="row" role="row" *cdkVirtualFor="let r of ds; let i = index" [class.alt]="i % 2">
              @if (r) {
                <span [class.best]="r.best" [title]="r.best ? 'Best path' : r.stale ? 'Stale path' : 'Not the best path'">
                  {{ r.best ? '*>' : r.stale ? 'S' : '*' }}
                </span>
                <span class="mono">{{ r.prefix }}</span>
                <span class="mono">{{ r.nextHop }}</span>
                <span class="mono clip" [title]="r.asPath ?? ''">{{ r.asPath || '–' }}</span>
                <span class="mono num">{{ r.med ?? '' }}</span>
                <span class="mono num">{{ r.localPref ?? '' }}</span>
                <span>{{ r.origin }}</span>
                <span [class]="'rpki-' + r.rpki">{{ r.rpki ?? '' }}</span>
                <span class="mono clip" [title]="communities(r)">{{ communities(r) }}</span>
                <span class="mono clip">{{ r.peer ? r.peer : 'local' }}</span>
                <span class="mono">{{ r.age | since: clock.now() }}</span>
              } @else {
                <span></span><span class="muted">Loading…</span>
              }
            </div>
        </cdk-virtual-scroll-viewport>
        @if (page()?.total === 0) {
          <p class="empty">No paths match. Clear a filter, or check that the table holds routes for this family.</p>
        }
      </section>
    </div>
  `,
  styles: `
    .filters {
      padding: 16px 16px 12px;
    }
    .summary {
      margin-left: auto;
    }
    .table {
      overflow-x: auto;
    }
    .row {
      display: grid;
      grid-template-columns: 34px 190px 190px minmax(200px, 1.4fr) 70px 80px 90px 80px minmax(160px, 1fr) 170px 100px;
      gap: 12px;
      align-items: center;
      height: 34px;
      padding: 0 12px;
      min-width: 1380px;
      white-space: nowrap;
    }
    .row.alt {
      background: var(--raised);
    }
    .head {
      font-size: 12px;
      font-weight: 500;
      color: var(--muted);
      border-bottom: 1px solid var(--line);
    }
    .clip {
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .best,
    .rpki-valid {
      color: var(--up);
      font-weight: 600;
    }
    .rpki-invalid {
      color: var(--down);
      font-weight: 600;
    }
    .rpki-not-found {
      color: var(--muted);
    }
    cdk-virtual-scroll-viewport {
      height: calc(100vh - 420px);
      min-height: 300px;
      min-width: 1380px;
    }
  `,
})
export default class RibPageComponent {
  protected readonly live = inject(Live);
  protected readonly clock = inject(Clock);
  private api = inject(Api);
  private viewport = viewChild(CdkVirtualScrollViewport);

  protected readonly form = inject(NonNullableFormBuilder).group({
    table: ['global' as RibQuery['table']],
    neighbor: [''],
    family: ['ipv4' as RibQuery['family']],
    prefix: ['', ipOrCidr],
    match: [''],
    nextHop: ['', ipAddress],
    asPath: ['', regex],
    community: [''],
    best: [''],
  });
  protected readonly loading = signal(false);
  protected readonly page = signal<RibPage | null>(null);
  protected readonly source = signal(this.open(this.form.getRawValue()));

  private open(query: RibQuery, refresh = false): RibDataSource {
    return new RibDataSource(this.api, query, refresh, (p) => this.page.set(p), (b) => this.loading.set(b));
  }

  protected err = (name: string) => errorText(this.form.get(name));
  protected communities = (r: RibRow) =>
    [...(r.communities ?? []), ...(r.extCommunities ?? []), ...(r.largeCommunities ?? [])].join(' ');

  /** Runs the filters against the daemon's snapshot; `refresh` re-reads the table first. */
  protected search(refresh = false) {
    if (this.form.invalid) return this.form.markAllAsTouched();
    const q = this.form.getRawValue();
    if (q.table !== 'global' && !q.neighbor) q.neighbor = this.live.neighbors()[0]?.address ?? '';
    this.form.patchValue({ neighbor: q.neighbor });
    this.page.set(null);
    this.viewport()?.scrollToIndex(0);
    this.source.set(this.open({ ...q, prefix: q.prefix.trim(), nextHop: q.nextHop.trim() }, refresh));
  }

  protected reset() {
    const { table, neighbor, family } = this.form.getRawValue();
    this.form.reset({ table, neighbor, family });
    this.search();
  }
}
