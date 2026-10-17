import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { Expense } from "./api";

const fixture: Expense = {
  id: "9007199254740993",
  user_id: "9223372036854775807",
  amount: "350.50",
  category: "еда",
  spent_at: "2026-10-16",
  comment: "Тестовый обед",
  created_at: "2026-10-16T09:00:00Z",
  updated_at: "2026-10-16T09:00:00Z",
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

type MockHandler = (
  url: URL,
  init: RequestInit,
) => Response | Promise<Response> | undefined;
let rows: Expense[];
let handler: MockHandler | undefined;
let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

beforeEach(() => {
  rows = [{ ...fixture }];
  handler = undefined;
  fetchMock = vi.fn<typeof fetch>(async (input, init = {}) => {
    const url = new URL(String(input), "http://localhost");
    const custom = handler?.(url, init);
    if (custom) return custom;
    const method = init.method ?? "GET";
    if (url.pathname === "/api/members")
      return json({
        items: [{ user_id: fixture.user_id }, { user_id: "202" }],
      });
    if (url.pathname === "/api/expenses" && method === "POST") {
      const created = {
        ...fixture,
        ...JSON.parse(String(init.body)),
        id: "43",
      };
      rows = [created, ...rows];
      return json(created, 201);
    }
    if (url.pathname === "/api/expenses")
      return json({
        items: rows,
        total: rows.length,
        limit: Number(url.searchParams.get("limit")),
        offset: Number(url.searchParams.get("offset")),
      });
    if (method === "PATCH") {
      rows = [{ ...rows[0], ...JSON.parse(String(init.body)) }];
      return json(rows[0]);
    }
    if (method === "DELETE") {
      rows = [];
      return new Response(null, { status: 204 });
    }
    return json(rows[0]);
  });
  vi.stubGlobal("fetch", fetchMock);
});

function request(method: string) {
  return fetchMock.mock.calls.find(([, init]) => init?.method === method);
}

function latestQuery() {
  const calls = fetchMock.mock.calls.filter(([url]) =>
    String(url).startsWith("/api/expenses?"),
  );
  return new URL(String(calls.at(-1)?.[0]), "http://localhost").searchParams;
}

async function openCreate() {
  await screen.findByText("Тестовый обед");
  await userEvent.click(
    screen.getByRole("button", { name: "+ Добавить расход" }),
  );
  return within(screen.getByRole("dialog"));
}

async function fillCreate() {
  const dialog = await openCreate();
  await userEvent.type(
    dialog.getByLabelText("Участник · Telegram ID *"),
    "303",
  );
  await userEvent.type(dialog.getByLabelText("Сумма *"), "999999999999.99");
  await userEvent.selectOptions(
    dialog.getByLabelText("Категория *"),
    "транспорт",
  );
  fireEvent.change(dialog.getByLabelText("Дата расхода *"), {
    target: { value: "2024-02-29" },
  });
  return dialog;
}

describe("expense journal", () => {
  it("renders exact amounts, IDs, calendar dates and HTML comments as text", async () => {
    rows[0].comment = "<script>window.hacked = true</script>";
    render(<App />);
    expect(await screen.findByText(rows[0].comment!)).toBeTruthy();
    expect(screen.getByRole("cell", { name: fixture.user_id })).toBeTruthy();
    expect(screen.getByRole("cell", { name: "350,50" })).toBeTruthy();
    expect(screen.getByText("16.10.2026")).toBeTruthy();
    expect(document.querySelector("tbody script")).toBeNull();
  });

  it("shows loading then an empty state", async () => {
    let finish!: (response: Response) => void;
    handler = (url) =>
      url.pathname === "/api/expenses"
        ? new Promise((resolve) => {
            finish = resolve;
          })
        : undefined;
    render(<App />);
    expect(screen.getByText("Загружаем расходы…")).toBeTruthy();
    await act(async () =>
      finish(json({ items: [], total: 0, limit: 20, offset: 0 })),
    );
    expect(await screen.findByText("Пока нет расходов")).toBeTruthy();
  });

  it("sends combined member/category/date filters to the API and resets them", async () => {
    render(<App />);
    await screen.findByText("Тестовый обед");
    await userEvent.selectOptions(screen.getByLabelText("Участник"), "202");
    await userEvent.selectOptions(screen.getByLabelText("Категория"), "еда");
    fireEvent.change(screen.getByLabelText("Дата с"), {
      target: { value: "2026-10-01" },
    });
    fireEvent.change(screen.getByLabelText("Дата по"), {
      target: { value: "2026-10-17" },
    });
    await waitFor(() =>
      expect(Object.fromEntries(latestQuery())).toEqual({
        user_id: "202",
        category: "еда",
        date_from: "2026-10-01",
        date_to: "2026-10-17",
        limit: "20",
        offset: "0",
        sort: "spent_at",
        order: "desc",
      }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Сбросить фильтры" }),
    );
    await waitFor(() => expect(latestQuery().has("user_id")).toBe(false));
    expect(latestQuery().has("category")).toBe(false);
    expect(latestQuery().has("date_from")).toBe(false);
    expect(latestQuery().has("date_to")).toBe(false);
  });

  it("uses server pagination/sorting and resets the page after a filter change", async () => {
    handler = (url) =>
      url.pathname === "/api/expenses"
        ? json({
            items: rows,
            total: 21,
            limit: 20,
            offset: Number(url.searchParams.get("offset")),
          })
        : undefined;
    render(<App />);
    await screen.findByText("Тестовый обед");
    await userEvent.click(screen.getByRole("button", { name: "Далее" }));
    await waitFor(() => expect(latestQuery().get("offset")).toBe("20"));
    await userEvent.selectOptions(
      screen.getByLabelText("Категория"),
      "покупки",
    );
    await waitFor(() => expect(latestQuery().get("offset")).toBe("0"));
    await userEvent.selectOptions(
      screen.getByLabelText("Сортировать"),
      "amount",
    );
    await userEvent.selectOptions(
      screen.getByLabelText("Порядок сортировки"),
      "asc",
    );
    await waitFor(() => expect(latestQuery().get("order")).toBe("asc"));
    expect(latestQuery().get("sort")).toBe("amount");
    await userEvent.selectOptions(
      screen.getByLabelText("Записей на странице"),
      "50",
    );
    await waitFor(() => expect(latestQuery().get("limit")).toBe("50"));
  });

  it("rejects a reversed filter interval before fetching", async () => {
    render(<App />);
    await screen.findByText("Тестовый обед");
    fireEvent.change(screen.getByLabelText("Дата с"), {
      target: { value: "2026-10-17" },
    });
    await screen.findByText("Тестовый обед");
    const count = fetchMock.mock.calls.length;
    fireEvent.change(screen.getByLabelText("Дата по"), {
      target: { value: "2026-10-01" },
    });
    expect(await screen.findByRole("alert")).toHaveProperty(
      "textContent",
      "Конец периода не может быть раньше начала.",
    );
    expect(fetchMock.mock.calls.length).toBe(count);
  });

  it("ignores a stale list response after filters change", async () => {
    let finish!: (response: Response) => void;
    handler = (url) =>
      url.pathname === "/api/expenses" && !url.searchParams.has("category")
        ? new Promise((resolve) => {
            finish = resolve;
          })
        : undefined;
    render(<App />);
    await userEvent.selectOptions(screen.getByLabelText("Категория"), "еда");
    await screen.findByText("Тестовый обед");
    await act(async () =>
      finish(json({ items: [], total: 0, limit: 20, offset: 0 })),
    );
    expect(screen.getByText("Тестовый обед")).toBeTruthy();
  });

  it("creates an expense with a new owner and an exact decimal string, then refreshes members/list", async () => {
    render(<App />);
    const dialog = await fillCreate();
    await userEvent.click(
      dialog.getByRole("button", { name: "Добавить расход" }),
    );
    await screen.findByText("Расход добавлен.");
    expect(JSON.parse(String(request("POST")?.[1]?.body))).toEqual({
      user_id: "303",
      amount: "999999999999.99",
      category: "транспорт",
      spent_at: "2024-02-29",
      comment: null,
    });
    expect(
      await screen.findByRole("cell", { name: /999\s999\s999\s999,99/ }),
    ).toBeTruthy();
    expect(
      fetchMock.mock.calls.filter(([url]) => url === "/api/members").length,
    ).toBe(2);
  });

  it("validates required fields and retains invalid values without a POST", async () => {
    render(<App />);
    const dialog = await openCreate();
    await userEvent.type(dialog.getByLabelText("Сумма *"), "0.001");
    await userEvent.type(
      dialog.getByLabelText("Участник · Telegram ID *"),
      "01",
    );
    await userEvent.click(
      dialog.getByRole("button", { name: "Добавить расход" }),
    );
    expect(await dialog.findByText("Проверьте поля формы.")).toBeTruthy();
    expect(dialog.getByLabelText("Сумма *")).toHaveProperty("value", "0.001");
    expect(request("POST")).toBeUndefined();
  });

  it("fetches current detail and PATCHes only the changed field", async () => {
    handler = (url, init) =>
      url.pathname.endsWith(fixture.id) && !init.method
        ? json({ ...fixture, comment: "Свежий комментарий" })
        : undefined;
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", {
        name: `Редактировать расход ${fixture.id}`,
      }),
    );
    const input = await screen.findByLabelText("Сумма *");
    expect(screen.getByLabelText("Комментарий")).toHaveProperty(
      "value",
      "Свежий комментарий",
    );
    await userEvent.clear(input);
    await userEvent.type(input, "450.5");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
    await screen.findByText("Изменения сохранены.");
    expect(request("PATCH")?.[0]).toBe(`/api/expenses/${fixture.id}`);
    expect(JSON.parse(String(request("PATCH")?.[1]?.body))).toEqual({
      amount: "450.50",
    });
  });

  it("clears a comment using explicit null and rejects an unchanged save", async () => {
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", {
        name: `Редактировать расход ${fixture.id}`,
      }),
    );
    await screen.findByLabelText("Комментарий");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
    expect(
      await screen.findByText("Нет изменений для сохранения."),
    ).toBeTruthy();
    expect(request("PATCH")).toBeUndefined();
    await userEvent.clear(screen.getByLabelText("Комментарий"));
    await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
    await screen.findByText("Изменения сохранены.");
    expect(JSON.parse(String(request("PATCH")?.[1]?.body))).toEqual({
      comment: null,
    });
  });

  it("shows server field errors without losing input or falsely confirming a save", async () => {
    handler = (_url, init) =>
      init.method === "POST"
        ? json(
            {
              error: {
                code: "validation_error",
                message: "Некорректные данные",
                fields: { amount: "Сумма отклонена сервером" },
              },
            },
            400,
          )
        : undefined;
    render(<App />);
    const dialog = await fillCreate();
    await userEvent.click(
      dialog.getByRole("button", { name: "Добавить расход" }),
    );
    expect(await dialog.findByText("Сумма отклонена сервером")).toBeTruthy();
    expect(dialog.getByLabelText("Сумма *")).toHaveProperty(
      "value",
      "999999999999.99",
    );
    expect(screen.queryByText("Расход добавлен.")).toBeNull();
  });

  it("requires confirmation, supports cancel, then deletes and refreshes", async () => {
    render(<App />);
    const remove = await screen.findByRole("button", {
      name: `Удалить расход ${fixture.id}`,
    });
    await userEvent.click(remove);
    expect(request("DELETE")).toBeUndefined();
    expect(document.activeElement).toBe(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Отмена",
      }),
    );
    await userEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Отмена",
      }),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    await userEvent.click(remove);
    await userEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Удалить",
      }),
    );
    await screen.findByText("Расход удалён.");
    expect(request("DELETE")?.[0]).toBe(`/api/expenses/${fixture.id}`);
    expect(await screen.findByText("Пока нет расходов")).toBeTruthy();
  });

  it("keeps the confirmation open after a delete error and allows retry", async () => {
    handler = (_url, init) =>
      init.method === "DELETE"
        ? json(
            {
              error: {
                code: "internal_error",
                message: "Внутренняя ошибка",
                fields: {},
              },
            },
            500,
          )
        : undefined;
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", {
        name: `Удалить расход ${fixture.id}`,
      }),
    );
    await userEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Удалить",
      }),
    );
    expect(await screen.findByText("Внутренняя ошибка")).toBeTruthy();
    expect(screen.queryByText("Расход удалён.")).toBeNull();
    handler = undefined;
    await userEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Удалить",
      }),
    );
    await screen.findByText("Расход удалён.");
  });

  it("recovers to the preceding page after deleting its last row", async () => {
    handler = (url) => {
      if (url.pathname !== "/api/expenses") return undefined;
      const offset = Number(url.searchParams.get("offset"));
      return json({
        items: rows.length ? rows : offset ? [] : [fixture],
        total: rows.length ? 21 : 20,
        limit: 20,
        offset,
      });
    };
    render(<App />);
    await screen.findByText("Тестовый обед");
    await userEvent.click(screen.getByRole("button", { name: "Далее" }));
    await waitFor(() => expect(latestQuery().get("offset")).toBe("20"));
    await userEvent.click(
      await screen.findByRole("button", {
        name: `Удалить расход ${fixture.id}`,
      }),
    );
    await userEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Удалить",
      }),
    );
    await screen.findByText("Расход удалён.");
    await waitFor(() => expect(latestQuery().get("offset")).toBe("0"));
    await screen.findByText("Тестовый обед");
  });

  it("shows a network error and retries the list", async () => {
    handler = (url) => {
      if (url.pathname === "/api/expenses")
        return Promise.reject(new TypeError("offline"));
    };
    render(<App />);
    expect(
      await screen.findByText(/Не удалось связаться с сервером/),
    ).toBeTruthy();
    handler = undefined;
    await userEvent.click(screen.getByRole("button", { name: "Повторить" }));
    expect(await screen.findByText("Тестовый обед")).toBeTruthy();
  });

  it("retries failed member and detail loads", async () => {
    handler = (url) =>
      url.pathname === "/api/members" || url.pathname.endsWith(fixture.id)
        ? json(
            {
              error: {
                code: "internal_error",
                message: "Внутренняя ошибка",
                fields: {},
              },
            },
            500,
          )
        : undefined;
    render(<App />);
    await screen.findByText(/Не удалось загрузить участников/);
    handler = undefined;
    await userEvent.click(
      screen.getByRole("button", { name: "Повторить загрузку участников" }),
    );
    await waitFor(() =>
      expect(screen.queryByText(/Не удалось загрузить участников/)).toBeNull(),
    );
    handler = (url) =>
      url.pathname.endsWith(fixture.id)
        ? json(
            {
              error: {
                code: "not_found",
                message: "Расход не найден",
                fields: {},
              },
            },
            404,
          )
        : undefined;
    await userEvent.click(
      await screen.findByRole("button", {
        name: `Редактировать расход ${fixture.id}`,
      }),
    );
    expect(await screen.findByText("Расход не найден")).toBeTruthy();
    handler = undefined;
    await userEvent.click(
      screen.getByRole("button", { name: "Повторить загрузку" }),
    );
    expect(await screen.findByLabelText("Сумма *")).toHaveProperty(
      "value",
      "350.50",
    );
  });
});
