import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { AbstractControl, NonNullableFormBuilder, ReactiveFormsModule, ValidationErrors, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { NamedSet, PrefixMatch, PrefixSet } from '../../core/models';
import { isCidr } from '../../shared/validators';

export type SetKind = 'prefixSets' | 'asPathSets' | 'communitySets' | 'extCommunitySets' | 'largeCommunitySets';

export const SET_KINDS: Record<SetKind, { title: string; one: string; hint: string; placeholder: string }> = {
  prefixSets: {
    title: 'Prefix sets',
    one: 'prefix set',
    hint: 'One prefix per line, optionally with a length range: 10.0.0.0/8 ge 16 le 24. One address family per set.',
    placeholder: '192.0.2.0/24\n10.0.0.0/8 le 24',
  },
  asPathSets: {
    title: 'AS path sets',
    one: 'AS path set',
    hint: 'One regular expression per line. _ matches a boundary: ^65001_ starts with, _65001$ originates from.',
    placeholder: '^65001_\n_64512$',
  },
  communitySets: {
    title: 'Community sets',
    one: 'community set',
    hint: 'One community or regular expression per line, such as 65000:100 or ^65000:.*$',
    placeholder: '65000:100',
  },
  extCommunitySets: {
    title: 'Extended community sets',
    one: 'extended community set',
    hint: 'One per line, with its type: rt:65000:100 or soo:65000:100',
    placeholder: 'rt:65000:100',
  },
  largeCommunitySets: {
    title: 'Large community sets',
    one: 'large community set',
    hint: 'One community or regular expression per line, such as 65000:1:2',
    placeholder: '65000:1:2',
  },
};

export interface SetEditorData {
  kind: SetKind;
  set: PrefixSet | NamedSet | null; // null adds a set
  taken: string[]; // names in use by other sets of this kind
}

const PREFIX_LINE = /^(\S+)(?:\s+ge\s+(\d+))?(?:\s+le\s+(\d+))?$/i;

function parsePrefix(line: string): PrefixMatch | null {
  const m = PREFIX_LINE.exec(line);
  if (!m || !isCidr(m[1])) return null;
  return { prefix: m[1], ge: m[2] ? +m[2] : undefined, le: m[3] ? +m[3] : undefined };
}

const lines = (v: string) => v.split('\n').map((l) => l.trim()).filter(Boolean);

@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [ReactiveFormsModule, MatDialogModule, MatButtonModule, MatFormFieldModule, MatInputModule],
  template: `
    <h2 mat-dialog-title>{{ data.set ? 'Edit' : 'Add' }} {{ kind.one }}</h2>
    <mat-dialog-content>
      <form [formGroup]="form" id="set" (ngSubmit)="save()" class="fields">
        <mat-form-field class="wide">
          <mat-label>Name</mat-label>
          <input matInput formControlName="name" />
          <mat-error>{{ nameError() }}</mat-error>
        </mat-form-field>
        <mat-form-field class="wide">
          <mat-label>Entries</mat-label>
          <textarea matInput formControlName="entries" rows="8" class="mono" [placeholder]="kind.placeholder"></textarea>
          <mat-hint>{{ kind.hint }}</mat-hint>
          <mat-error>{{ form.controls.entries.errors?.['line'] ?? 'Add at least one entry' }}</mat-error>
        </mat-form-field>
      </form>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button matButton mat-dialog-close>Cancel</button>
      <button matButton="filled" form="set">{{ data.set ? 'Save set' : 'Add set' }}</button>
    </mat-dialog-actions>
  `,
})
export class SetEditor {
  protected readonly data = inject<SetEditorData>(MAT_DIALOG_DATA);
  protected readonly kind = SET_KINDS[this.data.kind];
  private ref = inject(MatDialogRef<SetEditor, PrefixSet | NamedSet>);

  protected readonly form = inject(NonNullableFormBuilder).group({
    name: [
      this.data.set?.name ?? '',
      [
        Validators.required,
        Validators.pattern(/^[A-Za-z0-9_.:-]{1,64}$/),
        (c: AbstractControl) => (this.data.taken.includes(c.value) ? { taken: true } : null),
      ],
    ],
    entries: [this.initial(), [Validators.required, (c: AbstractControl) => this.checkLines(c.value)]],
  });

  private initial(): string {
    const s = this.data.set;
    if (!s) return '';
    if ('list' in s) return s.list.join('\n');
    return s.prefixes.map((p) => p.prefix + (p.ge ? ` ge ${p.ge}` : '') + (p.le ? ` le ${p.le}` : '')).join('\n');
  }

  private checkLines(value: string): ValidationErrors | null {
    for (const line of lines(value)) {
      let ok = true;
      if (this.data.kind === 'prefixSets') ok = !!parsePrefix(line);
      else if (this.data.kind === 'extCommunitySets') ok = /^(rt|soo):\S+$/i.test(line);
      else {
        try {
          new RegExp(line);
        } catch {
          ok = false;
        }
      }
      if (!ok) return { line: `Cannot read "${line}"` };
    }
    return null;
  }

  protected nameError(): string {
    const e = this.form.controls.name.errors;
    if (e?.['taken']) return 'Another set already has this name';
    return e?.['pattern'] ? 'Use letters, digits and . _ : - only' : 'Required';
  }

  protected save() {
    if (this.form.invalid) return this.form.markAllAsTouched();
    const { name, entries } = this.form.getRawValue();
    this.ref.close(
      this.data.kind === 'prefixSets'
        ? { name, prefixes: lines(entries).map((l) => parsePrefix(l)!) }
        : { name, list: lines(entries) },
    );
  }
}
