import { describe, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";

describe("fetch error handling", () => {
  it("handles a non-JSON proxy failure without rendering its raw body", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          new Response("<html>upstream unavailable</html>", { status: 502 }),
        ),
    );
    await expect(api.get("1")).rejects.toMatchObject({
      code: "invalid_response",
      status: 502,
    });
  });
  it("handles an unexpected JSON error envelope", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("{}", { status: 500 })),
    );
    await expect(api.get("1")).rejects.toMatchObject({
      code: "http_error",
      status: 500,
    });
  });
  it("keeps API field errors and string IDs intact", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              code: "validation_error",
              message: "Некорректные данные",
              fields: { user_id: "Неверный ID" },
            },
          }),
          { status: 400 },
        ),
      );
    vi.stubGlobal("fetch", fetch);
    await expect(api.get("9223372036854775807")).rejects.toEqual(
      new ApiError("Некорректные данные", 400, "validation_error", {
        user_id: "Неверный ID",
      }),
    );
    expect(fetch.mock.calls[0][0]).toBe("/api/expenses/9223372036854775807");
  });
});
