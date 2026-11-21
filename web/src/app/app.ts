import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { Live } from './core/live.service';
import { Theme } from './core/theme.service';
import { Icon, IconName } from './shared/icon';

const NAV: { path: string; label: string; icon: IconName }[] = [
  { path: 'dashboard', label: 'Dashboard', icon: 'dashboard' },
  { path: 'neighbors', label: 'Neighbors', icon: 'neighbors' },
  { path: 'rib', label: 'RIB explorer', icon: 'rib' },
  { path: 'inject', label: 'Route injection', icon: 'inject' },
  { path: 'policies', label: 'Policies', icon: 'policy' },
  { path: 'rpki', label: 'RPKI and BMP', icon: 'shield' },
];

@Component({
  selector: 'app-root',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [RouterOutlet, RouterLink, RouterLinkActive, MatButtonModule, MatTooltipModule, Icon],
  template: `
    <header>
      <button matIconButton class="menu" aria-label="Toggle navigation" (click)="open.set(!open())">
        <app-icon name="menu" />
      </button>
      <a class="brand" routerLink="/dashboard">bgpd</a>
      @if (live.status(); as st) {
        <span class="muted version">{{ st.version }}</span>
        <span class="identity mono">AS{{ st.asn }} <span class="muted">router ID</span> {{ st.routerId }}</span>
      }
      <span class="spacer"></span>
      <span class="link" [class.lost]="!live.connected()" role="status">
        {{ live.connected() ? 'Live' : 'Reconnecting…' }}
      </span>
      <button
        matIconButton
        (click)="theme.toggle()"
        [matTooltip]="theme.scheme() === 'dark' ? 'Use the light theme' : 'Use the dark theme'"
        [attr.aria-label]="theme.scheme() === 'dark' ? 'Use the light theme' : 'Use the dark theme'"
      >
        <app-icon [name]="theme.scheme() === 'dark' ? 'sun' : 'moon'" />
      </button>
    </header>
    <nav [class.open]="open()" aria-label="Main">
      @for (item of nav; track item.path) {
        <a [routerLink]="item.path" routerLinkActive="active" (click)="open.set(false)">
          <app-icon [name]="item.icon" />{{ item.label }}
        </a>
      }
    </nav>
    <main><router-outlet /></main>
  `,
  styles: `
    :host {
      display: grid;
      grid-template: 'header header' 52px 'nav main' 1fr / 208px minmax(0, 1fr);
      min-height: 100vh;
    }
    header {
      grid-area: header;
      display: flex;
      align-items: center;
      gap: 16px;
      padding: 0 12px 0 20px;
      background: var(--surface);
      border-bottom: 1px solid var(--line);
      position: sticky;
      top: 0;
      z-index: 5;
    }
    .brand {
      font: 600 17px var(--mono);
      color: var(--ink);
      text-decoration: none;
    }
    .version {
      font-size: 12px;
      margin-left: -8px;
    }
    .identity {
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .spacer {
      flex: 1;
    }
    .link {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      color: var(--up);
      font-size: 13px;
      white-space: nowrap;
    }
    .link::before {
      content: '';
      width: 8px;
      height: 8px;
      border-radius: 50%;
      background: currentColor;
    }
    .link.lost {
      color: var(--wait);
    }
    .menu {
      display: none;
    }
    nav {
      grid-area: nav;
      padding: 12px 8px;
      border-right: 1px solid var(--line);
      background: var(--surface);
      display: flex;
      flex-direction: column;
      gap: 2px;
    }
    nav a {
      display: flex;
      align-items: center;
      gap: 10px;
      padding: 8px 12px;
      border-radius: 4px;
      color: var(--muted);
      text-decoration: none;
      border-left: 3px solid transparent;
    }
    nav a:hover {
      color: var(--ink);
      background: var(--raised);
    }
    nav a.active {
      color: var(--ink);
      font-weight: 500;
      background: var(--raised);
      border-left-color: var(--primary);
    }
    main {
      grid-area: main;
      min-width: 0;
    }
    @media (max-width: 900px) {
      :host {
        grid-template: 'header' 52px 'main' 1fr / minmax(0, 1fr);
      }
      header {
        padding-left: 4px;
        gap: 8px;
      }
      .menu {
        display: inline-flex;
      }
      nav {
        display: none;
        position: fixed;
        inset: 52px 0 0 0;
        z-index: 4;
        border: 0;
      }
      nav.open {
        display: flex;
      }
    }
  `,
})
export class App {
  protected readonly live = inject(Live);
  protected readonly theme = inject(Theme);
  protected readonly nav = NAV;
  protected readonly open = signal(false);
}
