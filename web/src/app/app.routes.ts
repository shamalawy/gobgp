import { Routes } from '@angular/router';

export const routes: Routes = [
  { path: '', pathMatch: 'full', redirectTo: 'dashboard' },
  { path: 'dashboard', title: 'Dashboard', loadComponent: () => import('./features/dashboard/dashboard.page') },
  { path: 'neighbors', title: 'Neighbors', loadComponent: () => import('./features/neighbors/neighbors.page') },
  { path: 'rib', title: 'RIB explorer', loadComponent: () => import('./features/rib/rib.page') },
  { path: 'inject', title: 'Route injection', loadComponent: () => import('./features/inject/inject.page') },
  { path: 'policies', title: 'Policies', loadComponent: () => import('./features/policies/policies.page') },
  { path: 'rpki', title: 'RPKI and BMP', loadComponent: () => import('./features/rpki/rpki.page') },
  { path: '**', redirectTo: 'dashboard' },
];
