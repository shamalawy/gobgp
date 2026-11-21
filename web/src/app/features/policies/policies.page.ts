import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { NonNullableFormBuilder, ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatSelectModule } from '@angular/material/select';
import { MatSnackBar } from '@angular/material/snack-bar';
import { forkJoin } from 'rxjs';
import { Api } from '../../core/api.service';
import { Live } from '../../core/live.service';
import { Config, NamedSet, Policy, PolicyRef, PrefixSet, RouteMap, Statement } from '../../core/models';
import { confirm } from '../../shared/confirm';
import { Icon } from '../../shared/icon';
import { RouteMapEditor, RouteMapEditorData } from './route-map-editor';
import { SET_KINDS, SetEditor, SetEditorData, SetKind } from './set-editor';

@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [ReactiveFormsModule, MatButtonModule, MatFormFieldModule, MatSelectModule, Icon],
  template: `
    <div class="page">
      <div class="page-head">
        <div>
          <h1>Policies</h1>
          <p>
            Route maps filter and rewrite routes on import and export. Assign them to all neighbors here, or to one
            neighbor in its settings.
          </p>
        </div>
        <span class="spacer"></span>
        <button matButton="outlined" (click)="softResetAll()" [disabled]="!live.status()?.established">
          <app-icon name="refresh" /> Re-evaluate routes
        </button>
      </div>

      @if (config(); as cfg) {
        <section class="panel">
          <div class="panel-head">
            <h2>Route maps</h2>
            <span class="spacer"></span>
            <button matButton="filled" (click)="editMap(null)"><app-icon name="add" /> Add route map</button>
          </div>
          @for (m of cfg.policy.routeMaps; track m.name) {
            <article class="map">
              <header>
                <h3 class="mono">{{ m.name }}</h3>
                <span class="muted">{{ usage(m.name) }}</span>
                <span class="spacer"></span>
                <button matIconButton (click)="editMap(m)" [attr.aria-label]="'Edit ' + m.name"><app-icon name="edit" /></button>
                <button matIconButton (click)="removeMap(m)" [attr.aria-label]="'Delete ' + m.name"><app-icon name="delete" /></button>
              </header>
              <ol>
                @for (st of m.statements; track $index) {
                  <li>
                    <span>if {{ describeMatch(st) }}</span>
                    @if (describeSet(st); as changes) {
                      <span>set {{ changes }}</span>
                    }
                    <strong [class]="st.action || 'continue'">{{ st.action || 'continue' }}</strong>
                  </li>
                } @empty {
                  <li class="muted">No statements: every route falls through to the default action.</li>
                }
              </ol>
            </article>
          } @empty {
            <p class="empty">No route maps yet. Add one, then assign it below or on a neighbor.</p>
          }
        </section>

        <section class="panel">
          <div class="panel-head"><h2>Policy for all neighbors</h2></div>
          <form class="panel-body" [formGroup]="global" (ngSubmit)="saveGlobal()">
            <div class="fields">
              <mat-form-field>
                <mat-label>Import route maps</mat-label>
                <mat-select formControlName="importPolicies" multiple>
                  @for (m of cfg.policy.routeMaps; track m.name) {
                    <mat-option [value]="m.name">{{ m.name }}</mat-option>
                  }
                </mat-select>
              </mat-form-field>
              <mat-form-field>
                <mat-label>Then, for import</mat-label>
                <mat-select formControlName="importDefault">
                  <mat-option value="accept">Accept</mat-option>
                  <mat-option value="reject">Reject</mat-option>
                </mat-select>
              </mat-form-field>
              <mat-form-field>
                <mat-label>Export route maps</mat-label>
                <mat-select formControlName="exportPolicies" multiple>
                  @for (m of cfg.policy.routeMaps; track m.name) {
                    <mat-option [value]="m.name">{{ m.name }}</mat-option>
                  }
                </mat-select>
              </mat-form-field>
              <mat-form-field>
                <mat-label>Then, for export</mat-label>
                <mat-select formControlName="exportDefault">
                  <mat-option value="accept">Accept</mat-option>
                  <mat-option value="reject">Reject</mat-option>
                </mat-select>
              </mat-form-field>
            </div>
            <div class="actions">
              <button matButton="filled" [disabled]="global.pristine">Save policy for all neighbors</button>
              <span class="muted">A neighbor with its own route maps or default uses those instead.</span>
            </div>
          </form>
        </section>

        <div class="sets">
          @for (kind of kinds; track kind) {
            <section class="panel">
              <div class="panel-head">
                <h2>{{ info[kind].title }}</h2>
                <span class="spacer"></span>
                <button matButton (click)="editSet(kind, null)"><app-icon name="add" /> Add</button>
              </div>
              @for (s of cfg.policy[kind]; track s.name) {
                <article class="set">
                  <header>
                    <h3 class="mono">{{ s.name }}</h3>
                    <span class="spacer"></span>
                    <button matIconButton (click)="editSet(kind, s)" [attr.aria-label]="'Edit ' + s.name"><app-icon name="edit" /></button>
                    <button matIconButton (click)="removeSet(kind, s.name)" [attr.aria-label]="'Delete ' + s.name"><app-icon name="delete" /></button>
                  </header>
                  <p class="mono">{{ entries(s) }}</p>
                </article>
              } @empty {
                <p class="empty">None defined.</p>
              }
            </section>
          }
        </div>
      }
    </div>
  `,
  styles: `
    .map,
    .set {
      padding: 10px 16px 12px;
      border-bottom: 1px solid var(--line);
    }
    .map:last-child,
    .set:last-child {
      border-bottom: 0;
    }
    header {
      display: flex;
      align-items: center;
      gap: 12px;
    }
    header .spacer {
      flex: 1;
    }
    h3 {
      font-size: 14px;
    }
    ol {
      margin: 4px 0 0;
      padding-left: 22px;
    }
    li {
      padding: 3px 0;
    }
    li span {
      margin-right: 14px;
    }
    .accept {
      color: var(--up);
    }
    .reject {
      color: var(--down);
    }
    .continue {
      color: var(--muted);
    }
    .set p {
      margin: 2px 0 0;
      color: var(--muted);
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
    .sets {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
      gap: 20px;
      align-items: start;
    }
  `,
})
export default class PoliciesPage {
  private api = inject(Api);
  private dialog = inject(MatDialog);
  private snack = inject(MatSnackBar);
  protected readonly live = inject(Live);
  protected readonly config = signal<Config | null>(null);
  protected readonly kinds = Object.keys(SET_KINDS) as SetKind[];
  protected readonly info = SET_KINDS;

  protected readonly global = inject(NonNullableFormBuilder).group({
    importPolicies: [[] as string[]],
    importDefault: ['accept'],
    exportPolicies: [[] as string[]],
    exportDefault: ['accept'],
  });

  constructor() {
    this.load();
  }

  private load() {
    this.api.config().subscribe((cfg) => {
      this.config.set(cfg);
      this.global.reset({
        importPolicies: cfg.global.import?.policies ?? [],
        importDefault: cfg.global.import?.default || 'accept',
        exportPolicies: cfg.global.export?.policies ?? [],
        exportDefault: cfg.global.export?.default || 'accept',
      });
    });
  }

  /** Stores a changed policy document; on refusal the page reloads the stored one. */
  private savePolicy(change: (p: Policy) => void, done: string) {
    const policy: Policy = structuredClone(this.config()!.policy);
    change(policy);
    this.api.setPolicy(policy).subscribe({
      next: () => {
        this.snack.open(done, '', { duration: 3000 });
        this.load();
      },
      error: () => this.load(),
    });
  }

  protected entries(s: PrefixSet | NamedSet): string {
    if ('list' in s) return s.list.join('\n');
    return s.prefixes.map((p) => p.prefix + (p.ge ? ` ge ${p.ge}` : '') + (p.le ? ` le ${p.le}` : '')).join('\n');
  }

  protected describeMatch(st: Statement): string {
    const m = st.match;
    const parts = (
      [
        ['prefix', m.prefixSet], ['AS path', m.asPathSet], ['community', m.communitySet],
        ['ext community', m.extCommunitySet], ['large community', m.largeCommunitySet],
      ] as const
    )
      .filter(([, ref]) => ref)
      .map(([label, ref]) => `${label} ${ref!.match === 'invert' ? 'not ' : ''}in ${ref!.name}`);
    if (m.rpki) parts.push(`RPKI ${m.rpki}`);
    return parts.join(' and ') || 'any route';
  }

  protected describeSet(st: Statement): string {
    const s = st.set;
    const parts: string[] = [];
    if (s.localPref != null) parts.push(`local-pref ${s.localPref}`);
    if (s.med) parts.push(`MED ${{ set: '', add: '+', sub: '-' }[s.med.op]}${s.med.value}`);
    if (s.asPrepend) parts.push(`prepend ${s.asPrepend.asn || 'leftmost AS'} ×${s.asPrepend.repeat}`);
    if (s.nextHop) parts.push(`next-hop ${s.nextHop}`);
    for (const [label, a] of [['community', s.communities], ['ext community', s.extCommunities], ['large community', s.largeCommunities]] as const) {
      if (a) parts.push(`${a.op} ${label} ${a.values.join(' ')}`);
    }
    return parts.join(', ');
  }

  /** Where a route map is assigned, as shown next to its name. */
  protected usage(name: string): string {
    const cfg = this.config()!;
    const uses = (r?: PolicyRef) => !!r?.policies?.includes(name);
    const where = cfg.neighbors.filter((n) => uses(n.import) || uses(n.export)).map((n) => n.address);
    if (uses(cfg.global.import) || uses(cfg.global.export)) where.unshift('all neighbors');
    return where.length ? `used by ${where.join(', ')}` : 'not assigned';
  }

  protected editMap(map: RouteMap | null) {
    const policy = this.config()!.policy;
    const data: RouteMapEditorData = {
      map, policy, taken: policy.routeMaps.map((m) => m.name).filter((n) => n !== map?.name),
    };
    this.dialog
      .open<RouteMapEditor, RouteMapEditorData, RouteMap>(RouteMapEditor, {
        data, panelClass: 'sheet', position: { right: '0', top: '0' }, height: '100vh', width: '820px', maxWidth: '100vw',
      })
      .afterClosed()
      .subscribe((result) => {
        if (!result) return;
        // An edit keeps the name, so assignments stay valid.
        this.savePolicy((p) => {
          const i = p.routeMaps.findIndex((m) => m.name === map?.name);
          if (i >= 0) p.routeMaps[i] = { ...result, name: map!.name };
          else p.routeMaps.push(result);
        }, `Saved route map ${map?.name ?? result.name}`);
      });
  }

  protected removeMap(map: RouteMap) {
    confirm(this.dialog, {
      title: `Delete route map ${map.name}?`,
      message: 'A route map that is still assigned to a neighbor cannot be deleted.',
      action: 'Delete route map',
    }).subscribe(() =>
      this.savePolicy((p) => (p.routeMaps = p.routeMaps.filter((m) => m.name !== map.name)), `Deleted route map ${map.name}`),
    );
  }

  protected editSet(kind: SetKind, set: PrefixSet | NamedSet | null) {
    const sets: (PrefixSet | NamedSet)[] = this.config()!.policy[kind];
    const data: SetEditorData = { kind, set, taken: sets.map((s) => s.name).filter((n) => n !== set?.name) };
    this.dialog
      .open<SetEditor, SetEditorData, PrefixSet | NamedSet>(SetEditor, { data, width: '560px' })
      .afterClosed()
      .subscribe((result) => {
        if (!result) return;
        this.savePolicy((p) => {
          const list = p[kind] as (PrefixSet | NamedSet)[];
          const i = list.findIndex((s) => s.name === set?.name);
          if (i >= 0) list[i] = { ...result, name: set!.name };
          else list.push(result);
        }, `Saved ${this.info[kind].one} ${set?.name ?? result.name}`);
      });
  }

  protected removeSet(kind: SetKind, name: string) {
    confirm(this.dialog, {
      title: `Delete ${this.info[kind].one} ${name}?`,
      message: 'A set that a route map still matches on cannot be deleted.',
      action: 'Delete set',
    }).subscribe(() =>
      this.savePolicy((p) => {
        (p[kind] as (PrefixSet | NamedSet)[]) = (p[kind] as (PrefixSet | NamedSet)[]).filter((s) => s.name !== name);
      }, `Deleted ${this.info[kind].one} ${name}`),
    );
  }

  protected saveGlobal() {
    const v = this.global.getRawValue();
    const cfg = this.config()!;
    this.api
      .setGlobal({
        ...cfg.global,
        import: { policies: v.importPolicies, default: v.importDefault as PolicyRef['default'] },
        export: { policies: v.exportPolicies, default: v.exportDefault as PolicyRef['default'] },
      })
      .subscribe(() => {
        this.snack.open('Saved policy for all neighbors', '', { duration: 3000 });
        this.load();
      });
  }

  /** A policy change applies to new updates; this replays the existing routes through it. */
  protected softResetAll() {
    const up = this.live.neighbors().filter((n) => n.state === 'Established');
    forkJoin(up.map((n) => this.api.neighborAction(n.address, 'soft-reset', { direction: 'both' }))).subscribe(() =>
      this.snack.open(`Re-evaluating routes of ${up.length} neighbor${up.length === 1 ? '' : 's'}`, '', { duration: 3000 }),
    );
  }
}
