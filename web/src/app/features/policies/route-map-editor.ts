import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { AbstractControl, FormArray, NonNullableFormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { CommunityAction, Policy, RouteMap, SetRef, Statement } from '../../core/models';
import { Icon } from '../../shared/icon';
import { asn, communities, errorText, extCommunities, largeCommunities, words } from '../../shared/validators';

export interface RouteMapEditorData {
  map: RouteMap | null; // null adds a route map
  policy: Policy; // for the set names
  taken: string[];
}

const MATCHES = [
  { key: 'prefixSet', sets: 'prefixSets', label: 'Prefix set' },
  { key: 'asPathSet', sets: 'asPathSets', label: 'AS path set' },
  { key: 'communitySet', sets: 'communitySets', label: 'Community set' },
  { key: 'extCommunitySet', sets: 'extCommunitySets', label: 'Extended community set' },
  { key: 'largeCommunitySet', sets: 'largeCommunitySets', label: 'Large community set' },
] as const;

@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [ReactiveFormsModule, MatDialogModule, MatButtonModule, MatFormFieldModule, MatInputModule, MatSelectModule, Icon],
  template: `
    <h2 mat-dialog-title>{{ data.map ? 'Edit route map ' + data.map.name : 'Add route map' }}</h2>
    <mat-dialog-content>
      <form [formGroup]="form" id="routemap" (ngSubmit)="save()">
        <div class="fields">
          <mat-form-field>
            <mat-label>Name</mat-label>
            <input matInput formControlName="name" />
            <mat-error>{{ nameError() }}</mat-error>
          </mat-form-field>
        </div>
        <p class="muted">
          Statements run in order. The first one whose conditions all match and that accepts or rejects ends the route
          map; "continue" applies its changes and moves on.
        </p>
        <div formArrayName="statements">
          @for (st of statements.controls; track st; let i = $index) {
            <fieldset [formGroupName]="i">
              <legend>
                Statement {{ i + 1 }}
                <button matIconButton type="button" (click)="statements.removeAt(i)" [attr.aria-label]="'Remove statement ' + (i + 1)">
                  <app-icon name="delete" />
                </button>
              </legend>
              <h3>Match</h3>
              <div class="fields">
                @for (m of matches; track m.key) {
                  <mat-form-field>
                    <mat-label>{{ m.label }}</mat-label>
                    <mat-select [formControlName]="m.key">
                      <mat-option value="">Any route</mat-option>
                      @for (s of data.policy[m.sets]; track s.name) {
                        <mat-option [value]="s.name">{{ s.name }}</mat-option>
                      }
                    </mat-select>
                  </mat-form-field>
                  @if (st.get(m.key)?.value) {
                    <mat-form-field>
                      <mat-label>{{ m.label }} must match</mat-label>
                      <mat-select [formControlName]="m.key + 'Match'">
                        <mat-option value="any">Any entry</mat-option>
                        @if (m.key !== 'prefixSet' && m.key !== 'asPathSet') {
                          <mat-option value="all">All entries</mat-option>
                        }
                        <mat-option value="invert">No entry</mat-option>
                      </mat-select>
                    </mat-form-field>
                  }
                }
                <mat-form-field>
                  <mat-label>RPKI origin validation</mat-label>
                  <mat-select formControlName="rpki">
                    <mat-option value="">Any state</mat-option>
                    <mat-option value="valid">Valid</mat-option>
                    <mat-option value="invalid">Invalid</mat-option>
                    <mat-option value="not-found">Not found</mat-option>
                  </mat-select>
                </mat-form-field>
              </div>
              <h3>Change</h3>
              <div class="fields">
                <mat-form-field>
                  <mat-label>Local preference</mat-label>
                  <input matInput type="number" formControlName="localPref" />
                  <mat-error>{{ err(st, 'localPref') }}</mat-error>
                </mat-form-field>
                <mat-form-field>
                  <mat-label>MED</mat-label>
                  <mat-select formControlName="medOp">
                    <mat-option value="">Leave</mat-option>
                    <mat-option value="set">Set to</mat-option>
                    <mat-option value="add">Increase by</mat-option>
                    <mat-option value="sub">Decrease by</mat-option>
                  </mat-select>
                </mat-form-field>
                @if (st.get('medOp')?.value) {
                  <mat-form-field>
                    <mat-label>MED value</mat-label>
                    <input matInput type="number" formControlName="medValue" />
                    <mat-error>{{ err(st, 'medValue') }}</mat-error>
                  </mat-form-field>
                }
                <mat-form-field>
                  <mat-label>Prepend AS path (times)</mat-label>
                  <input matInput type="number" formControlName="prependRepeat" />
                  <mat-error>{{ err(st, 'prependRepeat') }}</mat-error>
                </mat-form-field>
                @if (st.get('prependRepeat')?.value) {
                  <mat-form-field>
                    <mat-label>AS to prepend</mat-label>
                    <input matInput formControlName="prependAsn" inputmode="numeric" />
                    <mat-hint>Empty repeats the leftmost AS</mat-hint>
                    <mat-error>{{ err(st, 'prependAsn') }}</mat-error>
                  </mat-form-field>
                }
                <mat-form-field>
                  <mat-label>Next hop</mat-label>
                  <input matInput formControlName="nextHop" class="mono" />
                  <mat-hint>"self", "unchanged" or an address</mat-hint>
                  <mat-error>Enter self, unchanged or an address</mat-error>
                </mat-form-field>
                @for (c of communityKinds; track c.key) {
                  <mat-form-field>
                    <mat-label>{{ c.label }}</mat-label>
                    <mat-select [formControlName]="c.key + 'Op'">
                      <mat-option value="">Leave</mat-option>
                      <mat-option value="add">Add</mat-option>
                      <mat-option value="remove">Remove</mat-option>
                      <mat-option value="replace">Replace with</mat-option>
                    </mat-select>
                  </mat-form-field>
                  @if (st.get(c.key + 'Op')?.value) {
                    <mat-form-field>
                      <mat-label>{{ c.label }} values</mat-label>
                      <input matInput [formControlName]="c.key" class="mono" [placeholder]="c.placeholder" />
                      <mat-error>{{ err(st, c.key) }}</mat-error>
                    </mat-form-field>
                  }
                }
              </div>
              <h3>Then</h3>
              <div class="fields">
                <mat-form-field>
                  <mat-label>Action</mat-label>
                  <mat-select formControlName="action">
                    <mat-option value="accept">Accept the route</mat-option>
                    <mat-option value="reject">Reject the route</mat-option>
                    <mat-option value="">Continue with the next statement</mat-option>
                  </mat-select>
                </mat-form-field>
              </div>
            </fieldset>
          }
        </div>
        <button matButton="outlined" type="button" (click)="statements.push(statement())">
          <app-icon name="add" /> Add statement
        </button>
      </form>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button matButton mat-dialog-close>Cancel</button>
      <button matButton="filled" form="routemap">{{ data.map ? 'Save route map' : 'Add route map' }}</button>
    </mat-dialog-actions>
  `,
  styles: `
    :host {
      display: flex;
      flex-direction: column;
      height: 100%;
    }
    mat-dialog-content {
      flex: 1;
      max-height: none;
    }
    fieldset {
      margin-bottom: 16px;
    }
    legend {
      display: flex;
      align-items: center;
      gap: 4px;
    }
    h3 {
      font-size: 12px;
      font-weight: 500;
      color: var(--muted);
      margin: 4px 0 8px;
    }
  `,
})
export class RouteMapEditor {
  protected readonly data = inject<RouteMapEditorData>(MAT_DIALOG_DATA);
  private ref = inject(MatDialogRef<RouteMapEditor, RouteMap>);
  private fb = inject(NonNullableFormBuilder);
  protected readonly matches = MATCHES;
  protected readonly communityKinds = [
    { key: 'communities', label: 'Communities', placeholder: '65000:100', check: communities },
    { key: 'extCommunities', label: 'Extended communities', placeholder: 'rt:65000:100', check: extCommunities },
    { key: 'largeCommunities', label: 'Large communities', placeholder: '65000:1:2', check: largeCommunities },
  ] as const;

  protected readonly form = this.fb.group({
    name: [
      this.data.map?.name ?? '',
      [
        Validators.required,
        Validators.pattern(/^[A-Za-z0-9_.:-]{1,64}$/),
        (c: AbstractControl) => (this.data.taken.includes(c.value) ? { taken: true } : null),
      ],
    ],
    statements: this.fb.array((this.data.map?.statements ?? [{ match: {}, set: {}, action: 'accept' }]).map((s) => this.statement(s))),
  });

  protected get statements() {
    return this.form.controls.statements as FormArray;
  }

  protected statement(s: Statement = { match: {}, set: {}, action: 'accept' }) {
    const nextHop = (c: AbstractControl) =>
      !c.value || c.value === 'self' || c.value === 'unchanged' || /^[0-9a-fA-F:.]+$/.test(c.value) ? null : { nextHop: true };
    return this.fb.group({
      prefixSet: [s.match.prefixSet?.name ?? ''],
      prefixSetMatch: [s.match.prefixSet?.match || 'any'],
      asPathSet: [s.match.asPathSet?.name ?? ''],
      asPathSetMatch: [s.match.asPathSet?.match || 'any'],
      communitySet: [s.match.communitySet?.name ?? ''],
      communitySetMatch: [s.match.communitySet?.match || 'any'],
      extCommunitySet: [s.match.extCommunitySet?.name ?? ''],
      extCommunitySetMatch: [s.match.extCommunitySet?.match || 'any'],
      largeCommunitySet: [s.match.largeCommunitySet?.name ?? ''],
      largeCommunitySetMatch: [s.match.largeCommunitySet?.match || 'any'],
      rpki: [s.match.rpki ?? ''],
      localPref: [s.set.localPref ?? (null as number | null), [Validators.min(0), Validators.max(4294967295)]],
      medOp: [s.set.med?.op ?? ''],
      medValue: [s.set.med?.value ?? 0, [Validators.min(0), Validators.max(4294967295)]],
      prependRepeat: [s.set.asPrepend?.repeat ?? (null as number | null), [Validators.min(1), Validators.max(32)]],
      prependAsn: [s.set.asPrepend?.asn ? String(s.set.asPrepend.asn) : '', asn],
      nextHop: [s.set.nextHop ?? '', nextHop],
      communitiesOp: [s.set.communities?.op ?? ''],
      communities: [(s.set.communities?.values ?? []).join(' '), communities],
      extCommunitiesOp: [s.set.extCommunities?.op ?? ''],
      extCommunities: [(s.set.extCommunities?.values ?? []).join(' '), extCommunities],
      largeCommunitiesOp: [s.set.largeCommunities?.op ?? ''],
      largeCommunities: [(s.set.largeCommunities?.values ?? []).join(' '), largeCommunities],
      action: [s.action ?? ''],
    });
  }

  protected err = (group: AbstractControl, name: string) => errorText(group.get(name));

  protected nameError(): string {
    const e = this.form.controls.name.errors;
    if (e?.['taken']) return 'Another route map already has this name';
    return e?.['pattern'] ? 'Use letters, digits and . _ : - only' : 'Required';
  }

  protected save() {
    if (this.form.invalid) return this.form.markAllAsTouched();
    const v = this.form.getRawValue();
    const ref = (name: string, match: string): SetRef | undefined => (name ? { name, match } : undefined);
    const community = (op: string, values: string): CommunityAction | undefined =>
      op ? { op: op as CommunityAction['op'], values: words(values) } : undefined;
    this.ref.close({
      name: v.name,
      statements: v.statements.map(
        (s): Statement => ({
          match: {
            prefixSet: ref(s.prefixSet, s.prefixSetMatch),
            asPathSet: ref(s.asPathSet, s.asPathSetMatch),
            communitySet: ref(s.communitySet, s.communitySetMatch),
            extCommunitySet: ref(s.extCommunitySet, s.extCommunitySetMatch),
            largeCommunitySet: ref(s.largeCommunitySet, s.largeCommunitySetMatch),
            rpki: s.rpki || undefined,
          },
          set: {
            localPref: s.localPref ?? undefined,
            med: s.medOp ? { op: s.medOp as 'set' | 'add' | 'sub', value: s.medValue } : undefined,
            asPrepend: s.prependRepeat ? { asn: +s.prependAsn || 0, repeat: s.prependRepeat } : undefined,
            nextHop: s.nextHop.trim() || undefined,
            communities: community(s.communitiesOp, s.communities),
            extCommunities: community(s.extCommunitiesOp, s.extCommunities),
            largeCommunities: community(s.largeCommunitiesOp, s.largeCommunities),
          },
          action: s.action as Statement['action'],
        }),
      ),
    });
  }
}
