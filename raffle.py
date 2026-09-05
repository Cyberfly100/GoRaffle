#%%
from secrets import choice
import tkinter as tk
from ctypes import windll
import json
from pathlib import Path
import logging
from typing import Callable

logging.basicConfig(filename=Path("./raffle.log"), level=logging.INFO)


class RaffleBackend:
    def __init__(self) -> None:
        self.score = {
            f"contestant {i+1}": {"count": 0, "exclude": False} for i in range(12)
        }
        self.exclude_list = []
        self.pick_hist = []
        self.winner = None
        self.savepath = Path("./raffle_memory.txt")

    def save_data(self) -> None:
        with open(self.savepath, "w") as file:
            json.dump({"score": self.score, "history": self.pick_hist}, file)

    def load_data(self) -> int:
        success = -1
        if self.savepath.exists():
            with open(self.savepath, "r") as file:
                data = json.load(file)
            score = data["score"]
            self.score = score
            self.pick_hist = data["history"]
            success = 1
        return success

    def add_name(self, name: str) -> dict:
        if name.lower() in self.score:
            logging.info(f"{name.capitalize()} is already participating.")
        else:
            self.score[name.lower()] = {
                "count": min([details["count"] for details in self.score.values()]),
                "exclude": False,
            }
        return self.score

    def remove_name(self, name: str) -> dict:
        if name.lower() in self.score:
            _ = self.score.pop(name.lower())
        else:
            logging.info(
                f"Could not remove {name.capitalize()}. Contestant not in list."
            )
        return self.score

    def pick_winner(self) -> str:
        logging.debug(f"name: {[name for name in self.score]}")
        logging.debug(f'count: {[details["count"] for details in self.score.values()]}')
        logging.debug(
            f'exclude: {[details["exclude"] for details in self.score.values()]}'
        )
        min_count = min(
            [
                details["count"]
                for details in self.score.values()
                if not details["exclude"]
            ]
        )
        valid_picks = [
            name
            for name, details in self.score.items()
            if details["count"] == min_count and not details["exclude"]
        ]
        self.winner = choice(valid_picks)
        self.pick_hist.append(self.winner)
        self.score[self.winner]["count"] += 1
        return self.winner.capitalize()

    def undo_pick(self) -> str:
        if not self.pick_hist:
            info = "Cannot undo any further."
        else:
            last_pick = self.pick_hist.pop()
            self.score[last_pick]["count"] -= 1
            logging.info(f"Removed last entry: {last_pick.capitalize()}")
            info = f"Removed last entry: {last_pick.capitalize()}"
        return info

    def reset_score(self) -> dict:
        for key in self.score:
            self.score[key]["count"] = 0
        self.pick_hist = []
        return self.score


class RaffleGUI:
    def __init__(self, master:tk.Tk) -> None:
        self.master = master

        self.visual_setup()
        self.button_setup()

        self.backend = RaffleBackend()
        self.backend.load_data()
        self.popup_ref = None

    def label_setup(self) -> None:
        self.label_text = tk.StringVar()
        self.label_text.set("Welcome to the draw!")

        self.label = tk.Label(
            self.master,
            textvariable=self.label_text,
            background="white",
            font=("Arial", 16),
        )
        self.label.pack(side="top")

    def visual_setup(self) -> None:
        self.master.overrideredirect(True)  # remove title bar
        self.master.after(
            20, lambda: self.set_window_appearance()
        )  # recover task bar icon

        self._offsetx = 200
        self._offsety = 200
        self.padding = 20
        self.width = 280
        self.height = 110

        self.master.configure(background="white")
        self.master.geometry(
            f"{self.width}x{self.height}+{self._offsetx}+{self._offsety}"
        )

        self.master.title("Raffle")

        self.label_setup()

        self.label.bind("<Button-1>", self.on_click_window)
        self.label.bind("<B1-Motion>", self.on_drag_window)

    @staticmethod
    def _button(owner, command:Callable, image:tk.PhotoImage=None, background:str="white") -> tk.Button:
        button = tk.Button(
            owner,
            relief="flat",
            highlightthickness=0,
            bd=0,
            background=background,
            activebackground="white",
            command=command,
        )
        if image==None:
            button.config(text="Close")
        else:
            button.config(image=image)
            button.image=image,
        return button

    def button_setup(self) -> None:
        raffle_img = tk.PhotoImage(file=Path("./pics/pick_winner.png"))
        undo_img = tk.PhotoImage(file=Path("./pics/undo.png"))
        config_img = tk.PhotoImage(file=Path("./pics/config.png"))

        self.button_row = tk.Frame(self.master, background="white")
        self.button_row.pack(side="top", pady=10)

        self.draw_button = self._button(
            owner = self.button_row,
            image=raffle_img,
            command=self.draw
        )

        self.undo_button = self._button(
            owner = self.button_row,
            image=undo_img,
            command=self.undo
        )

        self.popup_button = self._button(
            owner = self.button_row,
            image=config_img,
            command=self.popup
        )

        self.close_button = self._button(
            owner = self.master,
            command=self.quit,
        )

        left_button_xpadding = (20, int((self.width - 160) / 4))
        middle_button_xpadding = (int((self.width - 160) / 4), int((self.width - 160) / 4))
        right_button_xpadding = (int((self.width - 160) / 4), 20)
        
        xpaddings = [left_button_xpadding, middle_button_xpadding, right_button_xpadding]
        buttons = [self.undo_button, self.draw_button, self.popup_button]

        for button, xpadding in zip(buttons, xpaddings):
            button.pack(side="left", padx=xpadding)
            
        self.close_button.pack(side="top")

    def popup(self) -> None:
        if not self.popup_ref:
            self.popup_ref = ConfigPopup(self, self.master)

    def quit(self) -> None:
        self.backend.save_data()
        self.master.quit()  # use destroy for jupyter, else use quit.

    def on_drag_window(self, event) -> None:
        x = self.master.winfo_pointerx() - self.master._offsetx
        y = self.master.winfo_pointery() - self.master._offsety
        self.master.geometry("+{x}+{y}".format(x=x, y=y))

    def on_click_window(self, event) -> None:
        self.master._offsetx = (
            event.x + event.widget.winfo_rootx() - self.master.winfo_rootx()
        )
        self.master._offsety = (
            event.y + event.widget.winfo_rooty() - self.master.winfo_rooty()
        )

    def set_window_appearance(self) -> None:
        GWL_EXSTYLE = -20
        WS_EX_APPWINDOW = 0x00040000
        WS_EX_TOOLWINDOW = 0x00000080
        hwnd = windll.user32.GetParent(self.master.winfo_id())
        style = windll.user32.GetWindowLongPtrW(hwnd, GWL_EXSTYLE)
        style = style & ~WS_EX_TOOLWINDOW
        style = style | WS_EX_APPWINDOW
        res = windll.user32.SetWindowLongPtrW(hwnd, GWL_EXSTYLE, style)
        # re-assert the new window style
        self.master.wm_withdraw()
        self.master.after(10, lambda: self.master.wm_deiconify())

    def draw(self) -> None:
        if self.popup_ref:
            self.popup_ref.get_changes_from_table()
        milliseconds = 2250
        self.create_suspense_with_names(milliseconds)
        if self.popup_ref:
            self.popup_ref.get_changes_from_table()
        self.master.after(milliseconds, self.pick_winner)

    def pick_winner(self) -> None:
        winner = self.backend.pick_winner()
        self.label_text.set(f"The winner is {winner}!")
        if self.popup_ref:
            self.popup_ref.refresh_popup()
        logging.info(f"The winner is {winner}!")

    def create_suspense_with_dots(self, milliseconds:int) -> str:
        step = int(milliseconds / 9)
        for millis in range(0, milliseconds, step):
            dots = "".join(["."] * int((millis / step) % 3 + 1))
            info = f"The winner is {dots}"
            self.master.after(millis, self.label_text.set, info)
        return info

    def create_suspense_with_names(self, milliseconds:int) -> str:
        step = int(milliseconds / 30)
        for i, millis in enumerate(range(0, milliseconds, step)):
            names = [key.capitalize() for key in self.backend.score.keys()]
            info = f"The winner is {choice(names)}"
            self.master.after(millis, self.label_text.set, info)
        return info

    def undo(self) -> None:
        info = self.backend.undo_pick()
        if self.popup_ref:
            self.popup_ref.refresh_popup()
        self.label_text.set(info)
        score = self.backend.score
        [
            logging.debug(f'{name.capitalize()}: {details["count"]}')
            for name, details in score.items()
        ]


class ConfigPopup:
    def __init__(self, supernamespace, master:tk.Tk) -> None:
        self.master = master
        self.supernamespace = supernamespace
        self.popup = tk.Toplevel(self.master)

        self.visual_setup()
        self.button_setup()
        self.table_setup()

    def label_setup(self) -> None:
        self.label_text = tk.StringVar()
        self.label_text.set("Configuration")

        self.label = tk.Label(
            self.popup, textvariable=self.label_text, background="white"
        )
        self.label.place(bordermode="outside", height=20, width=self.width, x=0, y=0)

    def visual_setup(self) -> None:
        self.popup.overrideredirect(True)  # remove title bar.
        self.popup.after(
            10, lambda: self.set_window_appearance()
        )  # recover task bar icon.

        self._offsetx = 500
        self._offsety = 200
        self.padding = 20
        self.width = 200
        self.height = 300

        self.popup.configure(background="white")
        self.popup.geometry(
            f"{self.width}x{self.height}+{self._offsetx}+{self._offsety}"
        )

        self.popup.title("Configuration")

        self.label_setup()

        self.label.bind("<Button-1>", self.on_click_window)
        self.label.bind("<B1-Motion>", self.on_drag_window)

    def table_setup(self) -> None:
        self.wrapper = tk.LabelFrame(self.popup, background="white", borderwidth=0)
        self.scrollbox = tk.Canvas(
            self.wrapper,
            background="white",
            borderwidth=0,
            highlightthickness=0,
            width=180,
        )
        self.scrollbox.pack(side="left", fill="y")
        self.scrollbox.bind_all("<MouseWheel>", self._on_mousewheel)
        self.yscroll = tk.Scrollbar(
            self.wrapper, orient="vertical", command=self.scrollbox.yview
        )
        self.yscroll.pack(side="right", fill="y")
        self.scrollbox.configure(yscrollcommand=self.yscroll.set)
        self.scrollbox.bind("<Configure>", self.on_canvas_configure)
        self.table = tk.Frame(self.scrollbox, background="white", borderwidth=0)
        self.table_id = self.scrollbox.create_window(
            (0, 0), window=self.table, anchor="nw"
        )
        self.wrapper.pack(fill="x", expand="yes", padx=0, pady=20)
        self.modified_score_ref = self.makeform(self.supernamespace.backend.score)

    def _on_mousewheel(self, event):
        self.scrollbox.yview_scroll(int(-1 * (event.delta / 120)), "units")

    def on_canvas_configure(self, event):
        self.scrollbox.config(scrollregion=self.scrollbox.bbox("all"))
        # canvas_height = event.height
        # self.scrollbox.itemconfig(self.table_id, height = canvas_height)

    def makeform(self, score: dict) -> dict:
        entries = {}
        for name, details in score.items():
            row = tk.Frame(self.table, name=name, background="white")
            name_cell = tk.Entry(row, borderwidth=0)
            count_cell = tk.Entry(row, borderwidth=0, width=5)
            exclude_cell = tk.Checkbutton(
                row, onvalue=0, offvalue=1, background="white"
            )
            exclude_cell.var = tk.BooleanVar()
            exclude_cell.var.set(details["exclude"])
            exclude_cell.config(variable=exclude_cell.var)
            name_cell.insert(0, name.capitalize())
            count_cell.insert(0, details["count"])
            row.pack(side="top", fill="x", padx=5, pady=0)
            name_cell.pack(side="left")
            count_cell.pack(side="left")
            exclude_cell.pack(side="left", expand=1, fill="x")
            entries[name_cell] = {
                "count": count_cell,
                "exclude": exclude_cell.var,
            }
        return entries

    @staticmethod
    def _button(owner, command: Callable, image:tk.PhotoImage=None, background:str="white") -> tk.Button:
        button = tk.Button(
            owner,
            relief="flat",
            highlightthickness=0,
            bd=0,
            background=background,
            activebackground="white",
            command=command,
        )
        if image==None:
            button.config(text="Close")
        else:
            button.config(image=image)
            button.image=image,
        return button

    def button_setup(self) -> None:
        plus_img = tk.PhotoImage(file=Path("./pics/plus.png"))
        minus_img = tk.PhotoImage(file=Path("./pics/minus.png"))
        self.button_row = tk.Frame(self.popup, background="white")
        self.add_button = self._button(
            owner = self.button_row,
            image = plus_img,
            command = self.add_line
        )
        self.remove_button = self._button(
            owner = self.button_row,
            image = minus_img,
            command=self.remove_highlighted_name
        )
        self.close_button = self._button(
            owner = self.popup,
            command = self.quit
        )
        self.add_button.pack(
            side="left", padx=(0, 50)
        )
        self.remove_button.pack(
            side="right", padx=(50, 0)
        )
        self.close_button.pack(
            side="bottom"
        )
        self.button_row.pack(side="bottom")

    def score_from_ref(self) -> dict:
        ref = self.modified_score_ref
        modified_score = {
            name.get().lower(): {
                "count": int(details["count"].get()),
                "exclude": bool(int(details["exclude"].get())),
            }
            for name, details in ref.items()
        }
        return modified_score

    def get_changes_from_table(self) -> None:
        self.modified_score = self.score_from_ref()
        score_unmodified = self.supernamespace.backend.score == self.modified_score
        count_unmodified = [
            entry["count"] for entry in self.supernamespace.backend.score.values()
        ] == [entry["count"] for entry in self.modified_score.values()]
        names_unmodified = (
            self.supernamespace.backend.score.keys() == self.modified_score.keys()
        )
        if score_unmodified:
            pass
        elif count_unmodified and names_unmodified:
            self.supernamespace.backend.score = self.modified_score
        else:
            self.supernamespace.backend.score = self.modified_score
            self.supernamespace.backend.pick_hist = []

    def quit(self) -> None:
        self.get_changes_from_table()  # TODO: make save button to avoid overwriting of score during suspense
        self.popup.destroy()  # use destroy since we want to keep the main window.
        self.supernamespace.popup_ref = None

    def on_drag_window(self, event) -> None:
        x = self.popup.winfo_pointerx() - self.popup._offsetx
        y = self.popup.winfo_pointery() - self.popup._offsety
        self.popup.geometry("+{x}+{y}".format(x=x, y=y))

    def on_click_window(self, event) -> None:
        self.popup._offsetx = (
            event.x + event.widget.winfo_rootx() - self.popup.winfo_rootx()
        )
        self.popup._offsety = (
            event.y + event.widget.winfo_rooty() - self.popup.winfo_rooty()
        )

    def set_window_appearance(self) -> None:
        GWL_EXSTYLE = -20
        WS_EX_APPWINDOW = 0x00040000
        WS_EX_TOOLWINDOW = 0x00000080
        hwnd = windll.user32.GetParent(self.popup.winfo_id())
        style = windll.user32.GetWindowLongPtrW(hwnd, GWL_EXSTYLE)
        style = style & ~WS_EX_TOOLWINDOW
        style = style | WS_EX_APPWINDOW
        res = windll.user32.SetWindowLongPtrW(hwnd, GWL_EXSTYLE, style)
        # re-assert the new window style
        self.popup.wm_withdraw()
        self.popup.after(10, lambda: self.popup.wm_deiconify())

    def refresh_popup(self) -> None:
        if tk.Toplevel.winfo_exists(self.popup):
            self.wrapper.destroy()
            self.table_setup()
        else:
            pass

    def add_line(self) -> None:
        self.get_changes_from_table()
        self.supernamespace.backend.add_name("----")
        self.refresh_popup()

    def remove_empty_lines(self) -> None:
        self.get_changes_from_table()
        self.supernamespace.backend.remove_name("")
        self.supernamespace.backend.remove_name("----")
        self.refresh_popup()

    def remove_highlighted_name(self) -> None:
        highlighted_name = self.popup.focus_get().master.winfo_name()
        self.get_changes_from_table()
        if highlighted_name in self.supernamespace.backend.score.keys():
            self.supernamespace.backend.remove_name(highlighted_name)
        self.refresh_popup()


def main():
    root = tk.Tk()
    GUI = RaffleGUI(root)
    root.mainloop()


if __name__ == "__main__":
    main()
