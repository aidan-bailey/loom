// The unsent text the phone holds (spec Part 2 decision 3): a reply per session and an answer per
// ask, kept across tab switches for the page's lifetime as the classic app keeps them, dropped
// by the command that sends them on a 200, and dropped on unpair. Nothing survives a reload.
import { create } from "zustand";

// QuestionDraft is one question's unsent answer: the labels picked, in the order they were
// picked, the Other text and the note, all as typed.
export interface QuestionDraft {
  labels: string[];
  other: string;
  note: string;
}

// AskDraft is a question ask's unsent answers, keyed by the question's text.
export interface AskDraft {
  answers: Record<string, QuestionDraft>;
}

export interface DraftsState {
  replies: Record<number, string>;
  asks: Record<string, AskDraft>;
  // setReply keeps a session's reply as typed; the empty string deletes the session's entry.
  setReply: (session: number, text: string) => void;
  // setAsk replaces an ask's whole draft.
  setAsk: (ask: string, draft: AskDraft) => void;
  dropAsk: (ask: string) => void;
  // clear forgets every draft: the device was unpaired.
  clear: () => void;
}

export const useDrafts = create<DraftsState>()((set) => ({
  replies: {},
  asks: {},

  setReply(session, text) {
    set((state) => {
      if (text !== "") return { replies: { ...state.replies, [session]: text } };
      if (!(session in state.replies)) return state;
      const replies = { ...state.replies };
      delete replies[session];
      return { replies };
    });
  },

  setAsk(ask, draft) {
    set((state) => ({ asks: { ...state.asks, [ask]: draft } }));
  },

  dropAsk(ask) {
    set((state) => {
      if (!(ask in state.asks)) return state;
      const asks = { ...state.asks };
      delete asks[ask];
      return { asks };
    });
  },

  clear() {
    set({ replies: {}, asks: {} });
  },
}));
