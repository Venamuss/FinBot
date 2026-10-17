import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { Expense, Member } from "./api";

const expense: Expense = {
  id: "1",
  user_id: "101",
  amount: "350.50",
  category: "еда",
  spent_at: "2026-10-17",
  comment: "Синтетический расход",
  created_at: "2026-10-17T09:00:00Z",
  updated_at: "2026-10-17T09:00:00Z",
};
let members: Member[];
let failSave: boolean;
let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  members = [
    { user_id: "101", display_name: "Анна" },
    { user_id: "202", display_name: null },
  ];
  failSave = false;
  fetchMock = vi.fn<typeof fetch>(async (input, init = {}) => {
    const url = new URL(String(input), "http://localhost");
    if (init.method === "PUT") {
      if (failSave)
        return json(
          {
            error: {
              code: "validation_error",
              message: "Некорректные данные",
              fields: { display_name: "Имя отклонено сервером" },
            },
          },
          400,
        );
      const member: Member = {
        user_id: url.pathname.split("/").at(-1)!,
        display_name: JSON.parse(String(init.body)).display_name,
      };
      members = [
        ...members.filter((item) => item.user_id !== member.user_id),
        member,
      ];
      return json(member);
    }
    if (url.pathname === "/api/members") return json({ items: members });
    return json({ items: [expense], total: 1, limit: 20, offset: 0 });
  });
  vi.stubGlobal("fetch", fetchMock);
});

async function editName(id = "101") {
  await screen.findByRole("option", { name: "Анна · 101" });
  await userEvent.click(screen.getByText("Участники семьи"));
  await userEvent.click(
    screen.getByRole("button", { name: `Изменить имя участника ${id}` }),
  );
  return within(screen.getByRole("dialog"));
}

describe("member display names", () => {
  it("shows names and secondary IDs, while unnamed members use numeric IDs", async () => {
    render(<App />);
    expect(
      await screen.findByRole("option", { name: "Анна · 101" }),
    ).toHaveProperty("value", "101");
    expect(screen.getByRole("option", { name: "202" })).toHaveProperty(
      "value",
      "202",
    );
    const table = screen.getByRole("table");
    expect(
      within(table).getByRole("cell", { name: "Анна Telegram ID: 101" }),
    ).toBeTruthy();
    await userEvent.selectOptions(
      screen.getByLabelText("Участник", { exact: true }),
      "101",
    );
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(([url]) =>
          String(url).includes("user_id=101"),
        ),
      ).toBe(true),
    );
    expect(
      fetchMock.mock.calls.every(([url]) => !String(url).includes("Анна")),
    ).toBe(true);
  });

  it("trims and edits a name without changing its ID or expense ownership", async () => {
    render(<App />);
    const dialog = await editName();
    expect(dialog.queryByLabelText("Telegram ID *")).toBeNull();
    const input = dialog.getByLabelText("Отображаемое имя");
    await userEvent.clear(input);
    await userEvent.type(input, "  Мама  ");
    await userEvent.click(
      dialog.getByRole("button", { name: "Сохранить имя" }),
    );
    await screen.findByText("Имя участника сохранено.");
    expect(
      await screen.findByRole("option", { name: "Мама · 101" }),
    ).toBeTruthy();
    const call = fetchMock.mock.calls.find(
      ([, init]) => init?.method === "PUT",
    );
    expect(call?.[0]).toBe("/api/members/101");
    expect(JSON.parse(String(call?.[1]?.body))).toEqual({
      display_name: "Мама",
    });
    expect(
      within(screen.getByRole("table")).getByRole("cell", {
        name: "Мама Telegram ID: 101",
      }),
    ).toBeTruthy();
    expect(expense.user_id).toBe("101");
    expect(
      fetchMock.mock.calls.some(([, init]) => init?.method === "PATCH"),
    ).toBe(false);
  });

  it("clears names with null and restores the ID fallback", async () => {
    render(<App />);
    const dialog = await editName();
    await userEvent.clear(dialog.getByLabelText("Отображаемое имя"));
    await userEvent.click(
      dialog.getByRole("button", { name: "Сохранить имя" }),
    );
    await screen.findByText("Имя участника сохранено.");
    expect(await screen.findByRole("option", { name: "101" })).toBeTruthy();
    expect(
      within(screen.getByRole("table")).getByRole("cell", { name: "101" }),
    ).toBeTruthy();
    const call = fetchMock.mock.calls.find(
      ([, init]) => init?.method === "PUT",
    );
    expect(JSON.parse(String(call?.[1]?.body))).toEqual({ display_name: null });
  });

  it("names a discovered member and permits duplicate names for distinct IDs", async () => {
    render(<App />);
    const dialog = await editName("202");
    await userEvent.type(dialog.getByLabelText("Отображаемое имя"), "Анна");
    await userEvent.click(
      dialog.getByRole("button", { name: "Сохранить имя" }),
    );
    expect(
      await screen.findByRole("option", { name: "Анна · 202" }),
    ).toHaveProperty("value", "202");
    expect(screen.getByRole("option", { name: "Анна · 101" })).toHaveProperty(
      "value",
      "101",
    );
  });

  it("creates metadata for a new exact int64 ID before its first expense", async () => {
    render(<App />);
    await screen.findByRole("option", { name: "Анна · 101" });
    await userEvent.click(screen.getByText("Участники семьи"));
    await userEvent.click(
      screen.getByRole("button", { name: "Добавить участника" }),
    );
    const dialog = within(screen.getByRole("dialog"));
    await userEvent.type(
      dialog.getByLabelText("Telegram ID *"),
      "9223372036854775807",
    );
    await userEvent.type(dialog.getByLabelText("Отображаемое имя"), "Дедушка");
    await userEvent.click(
      dialog.getByRole("button", { name: "Сохранить имя" }),
    );
    expect(
      await screen.findByRole("option", {
        name: "Дедушка · 9223372036854775807",
      }),
    ).toHaveProperty("value", "9223372036854775807");
    expect(
      fetchMock.mock.calls.some(
        ([url, init]) =>
          url === "/api/members/9223372036854775807" && init?.method === "PUT",
      ),
    ).toBe(true);
  });

  it("validates length and keeps the form on server errors", async () => {
    render(<App />);
    const dialog = await editName();
    const input = dialog.getByLabelText("Отображаемое имя");
    await userEvent.clear(input);
    await userEvent.type(input, "я".repeat(101));
    await userEvent.click(
      dialog.getByRole("button", { name: "Сохранить имя" }),
    );
    expect(
      await dialog.findByText(/не более 100 символов без управляющих/),
    ).toBeTruthy();
    expect(
      fetchMock.mock.calls.some(([, init]) => init?.method === "PUT"),
    ).toBe(false);
    await userEvent.clear(input);
    await userEvent.type(input, "Мама");
    failSave = true;
    await userEvent.click(
      dialog.getByRole("button", { name: "Сохранить имя" }),
    );
    expect(await dialog.findByText("Имя отклонено сервером")).toBeTruthy();
    expect(input).toHaveProperty("value", "Мама");
    expect(screen.queryByText("Имя участника сохранено.")).toBeNull();
  });

  it("renders HTML names as text", async () => {
    members[0].display_name = "<script>window.injected=true</script>";
    render(<App />);
    await screen.findByRole("option", {
      name: "<script>window.injected=true</script> · 101",
    });
    expect(
      within(screen.getByRole("table")).getByText(members[0].display_name!),
    ).toBeTruthy();
    expect(document.querySelector("tbody script")).toBeNull();
  });
});
