import { HttpEventType } from '@angular/common/http';
import { ChangeDetectionStrategy, Component, computed, inject, signal } from '@angular/core';
import { NonNullableFormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Api } from '../../core/api.service';
import { Clock } from '../../core/clock.service';
import { Live } from '../../core/live.service';
import { InjectRequest, Route } from '../../core/models';
import { confirm } from '../../shared/confirm';
import { BytesPipe, CountPipe } from '../../shared/format';
import { Icon } from '../../shared/icon';
import { Sparkline } from '../../shared/sparkline';
import {
  asPath, asn, cidr, communities, errorText, extCommunities, ipAddress, largeCommunities, words,
} from '../../shared/validators';

@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    ReactiveFormsModule, MatButtonModule, MatButtonToggleModule, MatCheckboxModule, MatFormFieldModule, MatInputModule,
    MatProgressBarModule, MatSelectModule, Icon, Sparkline, CountPipe, BytesPipe,
  ],
  template: `
    <div class="page">
      <div class="page-head">
        <div>
          <h1>Route injection</h1>
          <p>Originate single routes by hand, or load a full routing table from an MRT dump or a CSV/JSON feed.</p>
        </div>
      </div>

      <section class="panel">
        <div class="panel-head"><h2>Originate a route</h2></div>
        <form class="panel-body" [formGroup]="route" (ngSubmit)="originate()">
          <div class="fields">
            <mat-form-field>
              <mat-label>Prefix</mat-label>
              <input matInput formControlName="prefix" class="mono" placeholder="203.0.113.0/24 or 2001:db8::/32" />
              <mat-error>{{ err(route, 'prefix') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Next hop</mat-label>
              <input matInput formControlName="nextHop" class="mono" />
              <mat-hint>Empty advertises this speaker as next hop</mat-hint>
              <mat-error>{{ err(route, 'nextHop') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>AS path</mat-label>
              <input matInput formControlName="asPath" class="mono" placeholder="65001 65002" />
              <mat-hint>The local AS is prepended on eBGP export</mat-hint>
              <mat-error>{{ err(route, 'asPath') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Origin</mat-label>
              <mat-select formControlName="origin">
                <mat-option value="igp">IGP</mat-option>
                <mat-option value="egp">EGP</mat-option>
                <mat-option value="incomplete">Incomplete</mat-option>
              </mat-select>
            </mat-form-field>
            <mat-form-field>
              <mat-label>MED</mat-label>
              <input matInput type="number" formControlName="med" />
              <mat-error>{{ err(route, 'med') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Local preference</mat-label>
              <input matInput type="number" formControlName="localPref" />
              <mat-error>{{ err(route, 'localPref') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Communities</mat-label>
              <input matInput formControlName="communities" class="mono" placeholder="65000:100 no-export" />
              <mat-error>{{ err(route, 'communities') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Extended communities</mat-label>
              <input matInput formControlName="extCommunities" class="mono" placeholder="rt:65000:100" />
              <mat-error>{{ err(route, 'extCommunities') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Large communities</mat-label>
              <input matInput formControlName="largeCommunities" class="mono" placeholder="65000:1:2" />
              <mat-error>{{ err(route, 'largeCommunities') }}</mat-error>
            </mat-form-field>
          </div>
          <div class="actions">
            <button matButton="filled">Originate route</button>
            <button matButton="outlined" type="button" (click)="withdraw(route.controls.prefix.value)">Withdraw prefix</button>
          </div>
        </form>
        @if (statics().length) {
          <div class="scroll-x">
            <table class="data">
              <thead>
                <tr>
                  <th>Static route</th><th>Next hop</th><th>AS path</th><th>Origin</th><th class="num">MED</th>
                  <th class="num">Local pref</th><th>Communities</th><th></th>
                </tr>
              </thead>
              <tbody>
                @for (r of statics(); track r.prefix) {
                  <tr>
                    <td class="mono">{{ r.prefix }}</td>
                    <td class="mono">{{ r.nextHop || 'self' }}</td>
                    <td class="mono">{{ r.asPath || '–' }}</td>
                    <td>{{ r.origin || 'igp' }}</td>
                    <td class="num mono">{{ r.med ?? '' }}</td>
                    <td class="num mono">{{ r.localPref ?? '' }}</td>
                    <td class="mono">{{ allCommunities(r) }}</td>
                    <td class="num">
                      <button matIconButton (click)="withdraw(r.prefix)" [attr.aria-label]="'Withdraw ' + r.prefix">
                        <app-icon name="delete" />
                      </button>
                    </td>
                  </tr>
                }
              </tbody>
            </table>
          </div>
        }
      </section>

      <section class="panel">
        <div class="panel-head">
          <h2>Inject a full table</h2>
          <span class="spacer"></span>
          <button matButton (click)="clear()" [disabled]="running()">Withdraw injected routes</button>
        </div>
        <form class="panel-body" [formGroup]="bulk" (ngSubmit)="start()">
          <mat-button-toggle-group formControlName="mode" aria-label="Where the table comes from" hideSingleSelectionIndicator>
            <mat-button-toggle value="upload">Upload a file</mat-button-toggle>
            <mat-button-toggle value="remote">URL or path on the server</mat-button-toggle>
          </mat-button-toggle-group>
          <div class="fields source">
            @if (bulk.value.mode === 'upload') {
              <div class="wide file">
                <button matButton="outlined" type="button" (click)="picker.click()">Choose file</button>
                <input #picker type="file" hidden (change)="file.set(picker.files?.[0] ?? null)" />
                <span class="mono">{{ file()?.name ?? 'No file chosen' }}</span>
                @if (file(); as f) {
                  <span class="muted">{{ f.size | bytes }}</span>
                }
              </div>
            } @else {
              <mat-form-field class="wide">
                <mat-label>Source</mat-label>
                <input matInput formControlName="source" class="mono" placeholder="https://data.ris.ripe.net/rrc00/latest-bview.gz" />
                <mat-hint>An http(s) URL, or a file path the daemon can read</mat-hint>
              </mat-form-field>
            }
            <mat-form-field>
              <mat-label>Format</mat-label>
              <mat-select formControlName="format">
                <mat-option value="">From the file name</mat-option>
                <mat-option value="mrt">MRT TABLE_DUMP_V2</mat-option>
                <mat-option value="csv">CSV</mat-option>
                <mat-option value="json">JSON</mat-option>
              </mat-select>
              <mat-hint>.gz and .bz2 are unpacked</mat-hint>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Address family</mat-label>
              <mat-select formControlName="family">
                <mat-option value="">IPv4 and IPv6</mat-option>
                <mat-option value="ipv4">IPv4 only</mat-option>
                <mat-option value="ipv6">IPv6 only</mat-option>
              </mat-select>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Rate limit (routes/s)</mat-label>
              <input matInput type="number" formControlName="rate" />
              <mat-hint>Empty for full speed</mat-hint>
              <mat-error>{{ err(bulk, 'rate') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Stop after (routes)</mat-label>
              <input matInput type="number" formControlName="limit" />
              <mat-error>{{ err(bulk, 'limit') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>MRT collector peer AS</mat-label>
              <input matInput formControlName="peerAs" inputmode="numeric" />
              <mat-hint>Empty takes the first path of each prefix</mat-hint>
              <mat-error>{{ err(bulk, 'peerAs') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>MRT collector peer address</mat-label>
              <input matInput formControlName="peerIp" class="mono" />
              <mat-error>{{ err(bulk, 'peerIp') }}</mat-error>
            </mat-form-field>
          </div>
          <fieldset>
            <legend>Rewrite attributes</legend>
            <div class="fields">
              <mat-checkbox formControlName="nextHopSelf">Set next hop to this speaker</mat-checkbox>
              <mat-form-field>
                <mat-label>Next hop</mat-label>
                <input matInput formControlName="nextHop" class="mono" />
                <mat-hint>Applies to routes of the same family</mat-hint>
                <mat-error>{{ err(bulk, 'nextHop') }}</mat-error>
              </mat-form-field>
              <mat-form-field>
                <mat-label>Prepend local AS (times)</mat-label>
                <input matInput type="number" formControlName="prepend" />
                <mat-error>{{ err(bulk, 'prepend') }}</mat-error>
              </mat-form-field>
              <mat-form-field>
                <mat-label>Local preference</mat-label>
                <input matInput type="number" formControlName="localPref" />
              </mat-form-field>
              <mat-form-field>
                <mat-label>MED</mat-label>
                <input matInput type="number" formControlName="med" />
              </mat-form-field>
              <mat-form-field>
                <mat-label>Add communities</mat-label>
                <input matInput formControlName="communities" class="mono" />
                <mat-error>{{ err(bulk, 'communities') }}</mat-error>
              </mat-form-field>
            </div>
          </fieldset>
          <div class="actions">
            <button matButton="filled" [disabled]="running() || uploading() !== null">Start injection</button>
            @if (running()) {
              <button matButton="outlined" type="button" (click)="api.cancelInject().subscribe()">Cancel injection</button>
            }
            @if (uploading() !== null) {
              <span class="muted">Uploading… {{ uploading() }} %</span>
            }
          </div>
        </form>

        @if (live.inject(); as st) {
          @if (st.state !== 'idle') {
            <div class="progress" role="status">
              <div class="progress-head">
                <strong [class]="st.state">{{ stateLabel() }}</strong>
                <span class="mono muted clip">{{ sourceName() }}</span>
              </div>
              <mat-progress-bar
                [mode]="st.state === 'running' && !st.bytesTotal ? 'indeterminate' : 'determinate'"
                [value]="percent()"
              />
              <dl>
                <div><dt>Routes injected</dt><dd class="mono">{{ st.routes | count }}</dd></div>
                <div><dt>Throughput</dt><dd class="mono">{{ throughput() | count }} routes/s</dd></div>
                <div><dt>Attribute sets shared</dt><dd class="mono">{{ st.attrSets | count }}</dd></div>
                <div><dt>Read</dt><dd class="mono">{{ st.bytesRead | bytes }}{{ st.bytesTotal ? ' of ' + (st.bytesTotal | bytes) : '' }}</dd></div>
                <div><dt>Elapsed</dt><dd class="mono">{{ elapsed() }} s</dd></div>
                <div><dt>Rejected</dt><dd class="mono">{{ st.failed | count }}</dd></div>
              </dl>
              @if (st.error) {
                <p class="failed">{{ st.error }}</p>
              }
              @if (live.rates().length > 1) {
                <app-sparkline [values]="live.rates()" label="Routes injected per second over the last minute" />
                <div class="axis muted"><span>Routes per second, last 60 s</span><span>peak {{ peak() | count }}</span></div>
              }
            </div>
          }
        }
      </section>
    </div>
  `,
  styles: `
    .actions {
      margin-top: 8px;
    }
    .source {
      margin-top: 16px;
    }
    .file {
      display: flex;
      align-items: center;
      gap: 12px;
      margin-bottom: 16px;
    }
    .progress {
      border-top: 1px solid var(--line);
      padding: 16px;
      display: grid;
      gap: 12px;
    }
    .progress-head {
      display: flex;
      gap: 12px;
      align-items: baseline;
    }
    .clip {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .running {
      color: var(--primary);
    }
    .done {
      color: var(--up);
    }
    .failed {
      color: var(--down);
      margin: 0;
    }
    .cancelled {
      color: var(--wait);
    }
    dl {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
      gap: 12px;
      margin: 0;
    }
    dt {
      color: var(--muted);
      font-size: 12px;
    }
    dd {
      margin: 0;
      font-size: 16px;
      font-weight: 500;
    }
    .axis {
      display: flex;
      justify-content: space-between;
      font-size: 12px;
    }
  `,
})
export default class InjectPage {
  protected readonly api = inject(Api);
  protected readonly live = inject(Live);
  private clock = inject(Clock);
  private dialog = inject(MatDialog);
  private snack = inject(MatSnackBar);
  private fb = inject(NonNullableFormBuilder);

  protected readonly statics = signal<Route[]>([]);
  protected readonly file = signal<File | null>(null);
  protected readonly uploading = signal<number | null>(null);

  protected readonly route = this.fb.group({
    prefix: ['', [Validators.required, cidr]],
    nextHop: ['', ipAddress],
    asPath: ['', asPath],
    origin: ['igp'],
    med: [null as number | null, [Validators.min(0), Validators.max(4294967295)]],
    localPref: [null as number | null, [Validators.min(0), Validators.max(4294967295)]],
    communities: ['', communities],
    extCommunities: ['', extCommunities],
    largeCommunities: ['', largeCommunities],
  });

  protected readonly bulk = this.fb.group({
    mode: ['upload' as 'upload' | 'remote'],
    source: [''],
    format: [''],
    family: [''],
    rate: [null as number | null, Validators.min(1)],
    limit: [null as number | null, Validators.min(1)],
    peerAs: ['', asn],
    peerIp: ['', ipAddress],
    nextHopSelf: [true],
    nextHop: ['', ipAddress],
    prepend: [null as number | null, [Validators.min(0), Validators.max(32)]],
    localPref: [null as number | null],
    med: [null as number | null],
    communities: ['', communities],
  });

  protected readonly running = computed(() => this.live.inject()?.state === 'running');
  protected readonly peak = computed(() => Math.max(0, ...this.live.rates()));
  protected readonly stateLabel = computed(
    () =>
      ({ idle: '', running: 'Injecting', done: 'Injection complete', failed: 'Injection failed', cancelled: 'Injection cancelled' })[
        this.live.inject()?.state ?? 'idle'
      ],
  );
  /** The source without the server's upload directory and temporary prefix. */
  protected readonly sourceName = computed(() => {
    const source = this.live.inject()?.source ?? '';
    return source.includes('://') ? source : source.replace(/^.*\//, '').replace(/^upload-\d+-/, '');
  });
  protected readonly percent = computed(() => {
    const st = this.live.inject();
    if (!st) return 0;
    if (st.state === 'done') return 100;
    return st.bytesTotal ? (100 * st.bytesRead) / st.bytesTotal : 0;
  });
  private readonly seconds = computed(() => {
    const st = this.live.inject();
    return st ? Math.max(((st.finished || this.clock.now()) - st.started) / 1000, 0) : 0;
  });
  protected readonly elapsed = computed(() => this.seconds().toFixed(1));
  /** The live rate while running, the average of the whole run afterwards. */
  protected readonly throughput = computed(() => {
    const st = this.live.inject();
    if (!st) return 0;
    return st.state === 'running' ? st.rate : st.routes / Math.max(this.seconds(), 0.001);
  });

  constructor() {
    this.loadStatics();
  }

  protected err = (form: { get(name: string): Parameters<typeof errorText>[0] }, name: string) => errorText(form.get(name));
  protected allCommunities = (r: Route) =>
    [...(r.communities ?? []), ...(r.extCommunities ?? []), ...(r.largeCommunities ?? [])].join(' ');

  private loadStatics() {
    this.api.config().subscribe((c) => this.statics.set(c.routes));
  }

  protected originate() {
    if (this.route.invalid) return this.route.markAllAsTouched();
    const v = this.route.getRawValue();
    const route: Route = {
      prefix: v.prefix.trim(),
      nextHop: v.nextHop.trim() || undefined,
      asPath: words(v.asPath).join(' ') || undefined,
      origin: v.origin,
      med: v.med ?? undefined,
      localPref: v.localPref ?? undefined,
      communities: words(v.communities),
      extCommunities: words(v.extCommunities),
      largeCommunities: words(v.largeCommunities),
    };
    this.api.addRoute(route).subscribe(() => {
      this.snack.open(`Originated ${route.prefix}`, '', { duration: 3000 });
      this.loadStatics();
    });
  }

  protected withdraw(prefix: string) {
    if (this.route.controls.prefix.invalid && prefix === this.route.controls.prefix.value) {
      return this.route.controls.prefix.markAsTouched();
    }
    this.api.deleteRoute(prefix.trim()).subscribe(() => {
      this.snack.open(`Withdrew ${prefix}`, '', { duration: 3000 });
      this.loadStatics();
    });
  }

  protected start() {
    if (this.bulk.invalid) return this.bulk.markAllAsTouched();
    const v = this.bulk.getRawValue();
    const req: InjectRequest = {
      source: v.source.trim(),
      format: v.format,
      family: v.family,
      rate: v.rate ?? 0,
      limit: v.limit ?? 0,
      peerAs: +v.peerAs || 0,
      peerIp: v.peerIp.trim(),
      nextHopSelf: v.nextHopSelf,
      nextHop: v.nextHop.trim(),
      prepend: v.prepend ?? 0,
      localPref: v.localPref,
      med: v.med,
      communities: words(v.communities),
    };
    if (v.mode === 'remote') {
      if (!req.source) return void this.snack.open('Enter a URL or a file path.', '', { duration: 4000 });
      return void this.api.inject(req).subscribe();
    }
    const file = this.file();
    if (!file) return void this.snack.open('Choose a file to upload.', '', { duration: 4000 });
    this.uploading.set(0);
    this.api.injectUpload(req, file).subscribe({
      next: (e) => {
        if (e.type === HttpEventType.UploadProgress && e.total) this.uploading.set(Math.round((100 * e.loaded) / e.total));
      },
      error: () => this.uploading.set(null),
      complete: () => this.uploading.set(null),
    });
  }

  protected clear() {
    confirm(this.dialog, {
      title: 'Withdraw injected routes?',
      message: 'Every bulk-injected route is withdrawn from the Loc-RIB and from all neighbors. Static routes stay.',
      action: 'Withdraw injected routes',
    }).subscribe(() => this.api.clearInjected().subscribe(() => this.snack.open('Withdrew injected routes', '', { duration: 3000 })));
  }
}
