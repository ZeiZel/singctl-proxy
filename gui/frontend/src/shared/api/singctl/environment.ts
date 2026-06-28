import { Environment } from "@wails/runtime/runtime";

// getPlatform returns the host OS ("darwin" | "linux" | "windows") via the Wails
// runtime, or "" when not running inside Wails (e.g. unit tests / plain browser).
export async function getPlatform(): Promise<string> {
  try {
    const environment = await Environment();
    return environment.platform;
  } catch {
    return "";
  }
}
