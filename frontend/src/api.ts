export const categories = [
  "еда",
  "транспорт",
  "жильё",
  "развлечения",
  "здоровье",
  "покупки",
  "другое",
] as const;
export type Category = (typeof categories)[number];
export type Sort = "spent_at" | "created_at" | "amount" | "id";
export type Order = "asc" | "desc";

export interface ExpenseInput {
  user_id: string;
  amount: string;
  category: Category;
  spent_at: string;
  comment?: string | null;
}

export interface Expense extends ExpenseInput {
  id: string;
  comment: string | null;
  created_at: string;
  updated_at: string;
}

export interface Member {
  user_id: string;
}
export interface ExpensePage {
  items: Expense[];
  total: number;
  limit: number;
  offset: number;
}

export interface ExpenseQuery {
  user_id: string;
  category: Category | "";
  date_from: string;
  date_to: string;
  sort: Sort;
  order: Order;
  limit: number;
  offset: number;
}

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code: string,
    public readonly fields: Record<string, string> = {},
  ) {
    super(message);
    this.name = "ApiError";
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api${path}`, {
      ...init,
      headers: init.body ? { "Content-Type": "application/json" } : undefined,
      credentials: "same-origin",
      cache: "no-store",
    });
  } catch (error) {
    if (init.signal?.aborted) throw error;
    throw new ApiError(
      "Не удалось связаться с сервером. Проверьте соединение и повторите попытку.",
      0,
      "network_error",
    );
  }

  if (response.status === 204 && response.ok) return undefined as T;
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw new ApiError(
      "Сервер вернул некорректный ответ. Повторите попытку.",
      response.status,
      "invalid_response",
    );
  }
  if (!response.ok) {
    const error = isRecord(body) ? body.error : undefined;
    if (
      isRecord(error) &&
      typeof error.message === "string" &&
      typeof error.code === "string"
    ) {
      const fields: Record<string, string> = {};
      if (isRecord(error.fields)) {
        for (const [key, value] of Object.entries(error.fields)) {
          if (typeof value === "string") fields[key] = value;
        }
      }
      throw new ApiError(error.message, response.status, error.code, fields);
    }
    throw new ApiError(
      "Не удалось выполнить запрос. Повторите попытку.",
      response.status,
      "http_error",
    );
  }
  return body as T;
}

export function errorMessage(error: unknown): string {
  return error instanceof ApiError
    ? error.message
    : "Не удалось выполнить операцию. Повторите попытку.";
}

export const api = {
  list(query: ExpenseQuery, signal?: AbortSignal) {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(query)) {
      if (value !== "") params.set(key, String(value));
    }
    return request<ExpensePage>(`/expenses?${params}`, { signal });
  },
  get(id: string, signal?: AbortSignal) {
    return request<Expense>(`/expenses/${encodeURIComponent(id)}`, { signal });
  },
  create(input: ExpenseInput) {
    return request<Expense>("/expenses", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },
  update(id: string, input: Partial<ExpenseInput>) {
    return request<Expense>(`/expenses/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify(input),
    });
  },
  delete(id: string) {
    return request<void>(`/expenses/${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
  },
  members(signal?: AbortSignal) {
    return request<{ items: Member[] }>("/members", { signal });
  },
};
