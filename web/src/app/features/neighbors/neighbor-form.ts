import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { NonNullableFormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { map } from 'rxjs';
import { Api } from '../../core/api.service';
import { NeighborConfig, PolicyRef } from '../../core/models';
import { asn, errorText, ipAddress, ipv4Address } from '../../shared/validators';

const optionalInt = (min: number, max: number) => [Validators.min(min), Validators.max(max)];
const num = (v: number | null) => v ?? undefined;

/** The add/edit sheet. `data` is the neighbor to edit, or null to add one. */
@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    ReactiveFormsModule, MatDialogModule, MatButtonModule, MatCheckboxModule, MatFormFieldModule, MatInputModule,
    MatSelectModule,
  ],
  template: `
    <h2 mat-dialog-title>{{ editing ? 'Edit neighbor ' + editing.address : 'Add neighbor' }}</h2>
    <mat-dialog-content>
      <form [formGroup]="form" id="neighbor" (ngSubmit)="save()">
        <div class="fields">
          <mat-form-field>
            <mat-label>Neighbor address</mat-label>
            <input matInput formControlName="address" class="mono" placeholder="192.0.2.1 or 2001:db8::1" />
            <mat-error>{{ err('address') }}</mat-error>
          </mat-form-field>
          <mat-form-field>
            <mat-label>Remote AS</mat-label>
            <input matInput formControlName="remoteAsn" inputmode="numeric" />
            <mat-hint>Same as the local AS for iBGP</mat-hint>
            <mat-error>{{ err('remoteAsn') }}</mat-error>
          </mat-form-field>
          <mat-form-field>
            <mat-label>Local AS override</mat-label>
            <input matInput formControlName="localAsn" inputmode="numeric" />
            <mat-error>{{ err('localAsn') }}</mat-error>
          </mat-form-field>
          <mat-form-field class="wide">
            <mat-label>Description</mat-label>
            <input matInput formControlName="description" maxlength="120" />
          </mat-form-field>
          <mat-checkbox formControlName="ipv4">IPv4 unicast</mat-checkbox>
          <mat-checkbox formControlName="ipv6">IPv6 unicast</mat-checkbox>
          <mat-checkbox formControlName="passive">Passive (wait for the peer to connect)</mat-checkbox>
          <mat-form-field>
            <mat-label>Local address</mat-label>
            <input matInput formControlName="localAddress" class="mono" />
            <mat-error>{{ err('localAddress') }}</mat-error>
          </mat-form-field>
          <mat-form-field>
            <mat-label>Remote port</mat-label>
            <input matInput type="number" formControlName="remotePort" placeholder="179" />
            <mat-error>{{ err('remotePort') }}</mat-error>
          </mat-form-field>
        </div>
        @if (!form.value.ipv4 && !form.value.ipv6) {
          <p class="problem">Select at least one address family.</p>
        }

        <fieldset>
          <legend>Timers</legend>
          <div class="fields">
            <mat-form-field>
              <mat-label>Hold time (s)</mat-label>
              <input matInput type="number" formControlName="holdTime" placeholder="90" />
              <mat-error>{{ err('holdTime') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Keepalive (s)</mat-label>
              <input matInput type="number" formControlName="keepalive" placeholder="30" />
              <mat-hint>At most a third of the hold time</mat-hint>
              <mat-error>{{ err('keepalive') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Connect retry (s)</mat-label>
              <input matInput type="number" formControlName="connectRetry" placeholder="120" />
              <mat-error>{{ err('connectRetry') }}</mat-error>
            </mat-form-field>
          </div>
        </fieldset>

        <fieldset>
          <legend>Session security</legend>
          <div class="fields">
            <mat-form-field>
              <mat-label>TCP authentication</mat-label>
              <mat-select formControlName="auth">
                <mat-option value="none">None</mat-option>
                <mat-option value="md5">MD5 signature (RFC 2385)</mat-option>
                <mat-option value="ao">TCP-AO (RFC 5925)</mat-option>
              </mat-select>
            </mat-form-field>
            @if (form.value.auth === 'md5') {
              <mat-form-field>
                <mat-label>MD5 password</mat-label>
                <input matInput type="password" formControlName="password" autocomplete="new-password" />
                <mat-error>{{ err('password') }}</mat-error>
              </mat-form-field>
            }
            @if (form.value.auth === 'ao') {
              <mat-form-field>
                <mat-label>Algorithm</mat-label>
                <mat-select formControlName="aoAlgorithm">
                  <mat-option value="hmac-sha1-96">HMAC-SHA-1-96</mat-option>
                  <mat-option value="aes-128-cmac-96">AES-128-CMAC-96</mat-option>
                  <mat-option value="hmac-sha256-96">HMAC-SHA-256-96</mat-option>
                  <mat-option value="hmac-sha256-128">HMAC-SHA-256-128</mat-option>
                </mat-select>
              </mat-form-field>
              <mat-form-field>
                <mat-label>Send ID</mat-label>
                <input matInput type="number" formControlName="aoSendId" />
                <mat-error>{{ err('aoSendId') }}</mat-error>
              </mat-form-field>
              <mat-form-field>
                <mat-label>Receive ID</mat-label>
                <input matInput type="number" formControlName="aoReceiveId" />
                <mat-error>{{ err('aoReceiveId') }}</mat-error>
              </mat-form-field>
              <mat-form-field>
                <mat-label>Master key</mat-label>
                <input matInput type="password" formControlName="aoKey" autocomplete="new-password" />
                <mat-error>{{ err('aoKey') }}</mat-error>
              </mat-form-field>
            }
            <mat-form-field>
              <mat-label>eBGP multihop TTL</mat-label>
              <input matInput type="number" formControlName="ebgpMultihop" />
              <mat-hint>Empty for a directly connected peer</mat-hint>
              <mat-error>{{ err('ebgpMultihop') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>GTSM minimum TTL</mat-label>
              <input matInput type="number" formControlName="ttlSecurityMin" placeholder="255" />
              <mat-hint>RFC 5082; not with multihop</mat-hint>
              <mat-error>{{ err('ttlSecurityMin') }}</mat-error>
            </mat-form-field>
            <mat-checkbox formControlName="bfd">BFD liveness detection</mat-checkbox>
            @if (form.value.bfd) {
              <mat-form-field>
                <mat-label>BFD transmit interval (ms)</mat-label>
                <input matInput type="number" formControlName="bfdMinTx" placeholder="300" />
              </mat-form-field>
              <mat-form-field>
                <mat-label>BFD receive interval (ms)</mat-label>
                <input matInput type="number" formControlName="bfdMinRx" placeholder="300" />
              </mat-form-field>
              <mat-form-field>
                <mat-label>BFD detection multiplier</mat-label>
                <input matInput type="number" formControlName="bfdMultiplier" placeholder="3" />
                <mat-error>{{ err('bfdMultiplier') }}</mat-error>
              </mat-form-field>
              <mat-checkbox formControlName="bfdMultihop">Multihop BFD</mat-checkbox>
            }
          </div>
        </fieldset>

        <fieldset>
          <legend>Route reflection and ADD-PATH</legend>
          <div class="fields">
            <mat-checkbox formControlName="routeReflectorClient">Route reflector client</mat-checkbox>
            <mat-form-field>
              <mat-label>Cluster ID</mat-label>
              <input matInput formControlName="clusterId" class="mono" />
              <mat-hint>Defaults to the router ID</mat-hint>
              <mat-error>{{ err('clusterId') }}</mat-error>
            </mat-form-field>
            <mat-checkbox formControlName="routeServerClient">Route server client</mat-checkbox>
            <mat-checkbox formControlName="addPathReceive">Receive multiple paths</mat-checkbox>
            <mat-form-field>
              <mat-label>Paths to send per prefix</mat-label>
              <input matInput type="number" formControlName="addPathSendMax" />
              <mat-hint>Empty sends only the best path</mat-hint>
              <mat-error>{{ err('addPathSendMax') }}</mat-error>
            </mat-form-field>
          </div>
        </fieldset>

        <fieldset>
          <legend>Graceful restart</legend>
          <div class="fields">
            <mat-checkbox formControlName="gr">Graceful restart (RFC 4724)</mat-checkbox>
            @if (form.value.gr) {
              <mat-form-field>
                <mat-label>Restart time (s)</mat-label>
                <input matInput type="number" formControlName="grRestartTime" placeholder="120" />
                <mat-error>{{ err('grRestartTime') }}</mat-error>
              </mat-form-field>
              <mat-checkbox formControlName="grHelperOnly">Helper only</mat-checkbox>
              <mat-checkbox formControlName="grNotification">Keep routes on NOTIFICATION (RFC 8538)</mat-checkbox>
              <mat-checkbox formControlName="llgr">Long-lived graceful restart (RFC 9494)</mat-checkbox>
              @if (form.value.llgr) {
                <mat-form-field>
                  <mat-label>Long-lived stale time (s)</mat-label>
                  <input matInput type="number" formControlName="llgrTime" />
                  <mat-error>{{ err('llgrTime') }}</mat-error>
                </mat-form-field>
              }
            }
          </div>
        </fieldset>

        <fieldset>
          <legend>Policy</legend>
          <div class="fields">
            <mat-form-field>
              <mat-label>Import route maps</mat-label>
              <mat-select formControlName="importPolicies" multiple>
                @for (m of routeMaps(); track m) {
                  <mat-option [value]="m">{{ m }}</mat-option>
                }
              </mat-select>
              <mat-hint>{{ routeMaps().length ? 'Applied in the order listed' : 'Create route maps under Policies' }}</mat-hint>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Then, for import</mat-label>
              <mat-select formControlName="importDefault">
                <mat-option value="">Use the global policy</mat-option>
                <mat-option value="accept">Accept</mat-option>
                <mat-option value="reject">Reject</mat-option>
              </mat-select>
            </mat-form-field>
            <span></span>
            <mat-form-field>
              <mat-label>Export route maps</mat-label>
              <mat-select formControlName="exportPolicies" multiple>
                @for (m of routeMaps(); track m) {
                  <mat-option [value]="m">{{ m }}</mat-option>
                }
              </mat-select>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Then, for export</mat-label>
              <mat-select formControlName="exportDefault">
                <mat-option value="">Use the global policy</mat-option>
                <mat-option value="accept">Accept</mat-option>
                <mat-option value="reject">Reject</mat-option>
              </mat-select>
            </mat-form-field>
            <span></span>
            <mat-form-field>
              <mat-label>Maximum prefixes</mat-label>
              <input matInput type="number" formControlName="maxPrefixes" />
              <mat-hint>Per address family; empty for no limit</mat-hint>
              <mat-error>{{ err('maxPrefixes') }}</mat-error>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Private AS numbers on export</mat-label>
              <mat-select formControlName="removePrivateAs">
                <mat-option value="">Keep</mat-option>
                <mat-option value="all">Remove</mat-option>
                <mat-option value="replace">Replace with the local AS</mat-option>
              </mat-select>
            </mat-form-field>
            <mat-form-field>
              <mat-label>Allow own AS in path</mat-label>
              <input matInput type="number" formControlName="allowOwnAs" />
              <mat-hint>Occurrences accepted; empty for none</mat-hint>
              <mat-error>{{ err('allowOwnAs') }}</mat-error>
            </mat-form-field>
          </div>
        </fieldset>
      </form>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button matButton mat-dialog-close>Cancel</button>
      <button matButton="filled" form="neighbor" [disabled]="saving()">
        {{ editing ? 'Save neighbor' : 'Add neighbor' }}
      </button>
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
    .problem {
      color: var(--down);
      margin: 0 0 8px;
    }
  `,
})
export class NeighborForm {
  private api = inject(Api);
  private ref = inject(MatDialogRef<NeighborForm>);
  protected readonly editing = inject<NeighborConfig | null>(MAT_DIALOG_DATA);
  protected readonly saving = signal(false);
  protected readonly routeMaps = toSignal(this.api.config().pipe(map((c) => c.policy.routeMaps.map((m) => m.name))), {
    initialValue: [],
  });

  private n: NeighborConfig = this.editing ?? { address: '', remoteAsn: 0 };
  protected readonly form = inject(NonNullableFormBuilder).group({
    address: [{ value: this.n.address, disabled: !!this.editing }, [Validators.required, ipAddress]],
    remoteAsn: [this.n.remoteAsn ? String(this.n.remoteAsn) : '', [Validators.required, asn]],
    localAsn: [this.n.localAsn ? String(this.n.localAsn) : '', asn],
    description: [this.n.description ?? ''],
    ipv4: [this.editing ? !!this.n.families?.includes('ipv4-unicast') : true],
    ipv6: [this.editing ? !!this.n.families?.includes('ipv6-unicast') : true],
    passive: [!!this.n.passive],
    localAddress: [this.n.localAddress ?? '', ipAddress],
    remotePort: [num(this.n.remotePort ?? null) ?? null, optionalInt(1, 65535)],
    holdTime: [this.n.holdTime ?? null, optionalInt(3, 65535)],
    keepalive: [this.n.keepalive ?? null, optionalInt(1, 21845)],
    connectRetry: [this.n.connectRetry ?? null, optionalInt(1, 65535)],
    auth: [this.n.tcpAo ? 'ao' : this.n.password ? 'md5' : 'none'],
    password: [this.n.password ?? ''],
    aoAlgorithm: [this.n.tcpAo?.algorithm ?? 'hmac-sha256-128'],
    aoSendId: [this.n.tcpAo?.sendId ?? 1, optionalInt(0, 255)],
    aoReceiveId: [this.n.tcpAo?.receiveId ?? 1, optionalInt(0, 255)],
    aoKey: [this.n.tcpAo?.key ?? ''],
    ebgpMultihop: [this.n.ebgpMultihop ?? null, optionalInt(1, 255)],
    ttlSecurityMin: [this.n.ttlSecurityMin ?? null, optionalInt(1, 255)],
    bfd: [!!this.n.bfd],
    bfdMinTx: [this.n.bfd?.minTx ?? null, optionalInt(1, 60000)],
    bfdMinRx: [this.n.bfd?.minRx ?? null, optionalInt(1, 60000)],
    bfdMultiplier: [this.n.bfd?.multiplier ?? null, optionalInt(1, 255)],
    bfdMultihop: [!!this.n.bfd?.multihop],
    routeReflectorClient: [!!this.n.routeReflectorClient],
    clusterId: [this.n.clusterId ?? '', ipv4Address],
    routeServerClient: [!!this.n.routeServerClient],
    addPathReceive: [!!this.n.addPathReceive],
    addPathSendMax: [this.n.addPathSendMax ?? null, optionalInt(1, 255)],
    gr: [!!this.n.gracefulRestart],
    grRestartTime: [this.n.gracefulRestart?.restartTime ?? null, optionalInt(1, 4095)],
    grHelperOnly: [!!this.n.gracefulRestart?.helperOnly],
    grNotification: [!!this.n.gracefulRestart?.notification],
    llgr: [!!this.n.gracefulRestart?.longLived],
    llgrTime: [this.n.gracefulRestart?.llgrTime ?? null, optionalInt(1, 16777215)],
    importPolicies: [this.n.import?.policies ?? []],
    importDefault: [this.n.import?.default ?? ''],
    exportPolicies: [this.n.export?.policies ?? []],
    exportDefault: [this.n.export?.default ?? ''],
    maxPrefixes: [this.n.maxPrefixes ?? null, optionalInt(1, 4294967295)],
    removePrivateAs: [this.n.removePrivateAs ?? ''],
    allowOwnAs: [this.n.allowOwnAs ?? null, optionalInt(1, 255)],
  });

  protected err = (name: string) => errorText(this.form.get(name));

  protected save() {
    const v = this.form.getRawValue();
    const missingSecret = (v.auth === 'md5' && !v.password) || (v.auth === 'ao' && !v.aoKey);
    if (missingSecret) this.form.get(v.auth === 'md5' ? 'password' : 'aoKey')!.setErrors({ required: true });
    if (this.form.invalid || (!v.ipv4 && !v.ipv6)) return this.form.markAllAsTouched();

    const ref = (policies: string[], def: string): PolicyRef | undefined =>
      policies.length || def ? { policies, default: def as PolicyRef['default'] } : undefined;
    const neighbor: NeighborConfig = {
      address: v.address.trim(),
      remoteAsn: +v.remoteAsn,
      localAsn: v.localAsn ? +v.localAsn : undefined,
      description: v.description.trim() || undefined,
      adminDown: this.n.adminDown,
      families: [...(v.ipv4 ? ['ipv4-unicast'] : []), ...(v.ipv6 ? ['ipv6-unicast'] : [])],
      passive: v.passive || undefined,
      localAddress: v.localAddress.trim() || undefined,
      remotePort: num(v.remotePort),
      holdTime: num(v.holdTime),
      keepalive: num(v.keepalive),
      connectRetry: num(v.connectRetry),
      password: v.auth === 'md5' ? v.password : undefined,
      tcpAo:
        v.auth === 'ao'
          ? { sendId: v.aoSendId ?? 0, receiveId: v.aoReceiveId ?? 0, algorithm: v.aoAlgorithm, key: v.aoKey }
          : undefined,
      ebgpMultihop: num(v.ebgpMultihop),
      ttlSecurityMin: num(v.ttlSecurityMin),
      bfd: v.bfd
        ? { minTx: num(v.bfdMinTx), minRx: num(v.bfdMinRx), multiplier: num(v.bfdMultiplier), multihop: v.bfdMultihop }
        : undefined,
      routeReflectorClient: v.routeReflectorClient || undefined,
      clusterId: v.clusterId.trim() || undefined,
      routeServerClient: v.routeServerClient || undefined,
      addPathReceive: v.addPathReceive || undefined,
      addPathSendMax: num(v.addPathSendMax),
      gracefulRestart: v.gr
        ? {
            restartTime: num(v.grRestartTime),
            helperOnly: v.grHelperOnly,
            notification: v.grNotification,
            longLived: v.llgr,
            llgrTime: v.llgr ? num(v.llgrTime) : undefined,
          }
        : undefined,
      import: ref(v.importPolicies, v.importDefault),
      export: ref(v.exportPolicies, v.exportDefault),
      maxPrefixes: num(v.maxPrefixes),
      removePrivateAs: v.removePrivateAs as NeighborConfig['removePrivateAs'],
      allowOwnAs: num(v.allowOwnAs),
    };
    this.saving.set(true);
    (this.editing ? this.api.updateNeighbor(neighbor) : this.api.addNeighbor(neighbor)).subscribe({
      next: () => this.ref.close(true),
      error: () => this.saving.set(false),
    });
  }
}
