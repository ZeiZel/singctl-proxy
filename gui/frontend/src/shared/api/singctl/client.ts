import * as App from "@wails/go/bridge/App";

import {
  type TApplication,
  type TInstalledApp,
  type TKey,
  type TLicenseInfo,
  type TMode,
  type TProcInfo,
  type TProxiedApp,
  type TSettings,
  type TStatus,
} from "./types";

// api is the typed wrapper over the generated Wails bindings. It is the single
// gateway to the Go bridge; only this slice and the live store import "@wails".
export const api = {
  getStatus: (): Promise<TStatus> => App.GetStatus(),
  setMode: (mode: TMode): Promise<void> => App.SetMode(mode),
  getKeys: (): Promise<TKey[]> => App.GetKeys(),
  addKey: (link: string): Promise<void> => App.AddKey(link),
  renameKey: (index: number, name: string): Promise<void> => App.RenameKey(index, name),
  deleteKey: (index: number): Promise<void> => App.DeleteKey(index),
  getSettings: (): Promise<TSettings> => App.GetSettings(),
  applySettings: (settings: TSettings): Promise<void> => App.ApplySettings(settings),
  listProcesses: (): Promise<TProcInfo[]> => App.ListProcesses(),
  listRouted: (): Promise<number[]> => App.ListRouted(),
  routePID: (pid: number): Promise<void> => App.RoutePID(pid),
  unroutePID: (pid: number): Promise<void> => App.UnroutePID(pid),
  killPID: (pid: number): Promise<void> => App.KillPID(pid),
  restartPID: (pid: number): Promise<number> => App.RestartPID(pid),
  listApplications: (): Promise<TApplication[]> => App.ListApplications(),
  listRoutedApps: (): Promise<string[]> => App.ListRoutedApps(),
  routeApp: (bundleID: string): Promise<void> => App.RouteApp(bundleID),
  unrouteApp: (bundleID: string): Promise<void> => App.UnrouteApp(bundleID),
  launchApp: (argv: string[]): Promise<number> => App.LaunchApp(argv),
  listInstalledApps: (): Promise<TInstalledApp[]> => App.ListInstalledApps(),
  listProxiedApps: (): Promise<TProxiedApp[]> => App.ListProxiedApps(),
  launchAppBundle: (path: string): Promise<number> => App.LaunchAppBundle(path),
  setAppEnabled: (bundleID: string, enabled: boolean): Promise<void> => App.SetAppEnabled(bundleID, enabled),
  removeApp: (bundleID: string): Promise<void> => App.RemoveApp(bundleID),
  stopDaemon: (): Promise<void> => App.StopDaemon(),
  getLicense: (): Promise<TLicenseInfo> => App.GetLicense(),
  activateLicense: (token: string, email: string): Promise<void> => App.ActivateLicense(token, email),
  removeLicense: (): Promise<void> => App.RemoveLicense(),
};
