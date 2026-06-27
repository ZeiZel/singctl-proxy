import { beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/shared/api/singctl";

import { stopDaemon } from "./stopDaemon";

vi.mock("@/shared/api/singctl", () => ({ api: { stopDaemon: vi.fn() } }));

const mockedStop = vi.mocked(api.stopDaemon);

beforeEach(() => mockedStop.mockReset());

describe("stopDaemon", () => {
  it("stops the daemon when confirmed", async () => {
    mockedStop.mockResolvedValue();
    await stopDaemon(() => true);
    expect(mockedStop).toHaveBeenCalledOnce();
  });

  it("does nothing when the user cancels", async () => {
    await stopDaemon(() => false);
    expect(mockedStop).not.toHaveBeenCalled();
  });
});
