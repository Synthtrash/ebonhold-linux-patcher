import Lean

open Lean

namespace ProfileIsolation

-- Paths are resolved, absolute path components. `none` is an unset folder.
-- Filesystem identity and symlink resolution are checked by the Go launcher.
abbrev Path := List String
abbrev Folder := Option Path

inductive Profile where
  | regular
  | ptr

def Profile.other : Profile → Profile
  | .regular => .ptr
  | .ptr => .regular

structure Folders where
  regular : Folder
  ptr : Folder
  deriving ToJson

def Folders.forProfile (f : Folders) : Profile → Folder
  | .regular => f.regular
  | .ptr => f.ptr

def Folders.withFolder (f : Folders) (profile : Profile) (folder : Folder) : Folders :=
  match profile with
  | .regular => { f with regular := folder }
  | .ptr => { f with ptr := folder }

def Folders.separate (f : Folders) : Bool :=
  match f.regular, f.ptr with
  | some regular, some ptr => !(regular.isPrefixOf ptr || ptr.isPrefixOf regular)
  | _, _ => true

structure State where
  profile : Profile
  folders : Folders

def State.activeFolder (s : State) : Folder := s.folders.forProfile s.profile

inductive Edit where
  | switch (profile : Profile)
  | setFolder (folder : Folder)

def State.tryEdit (s : State) : Edit → Option State
  | .switch profile => some { s with profile := profile }
  | .setFolder folder =>
    let folders := s.folders.withFolder s.profile folder
    if folders.separate then some { s with folders := folders } else none

def State.applyEdit (s : State) (edit : Edit) : State := (s.tryEdit edit).getD s

theorem switchLeavesFolders (s : State) (profile : Profile) :
    (s.applyEdit (.switch profile)).folders = s.folders ∧
    (s.applyEdit (.switch profile)).activeFolder = s.folders.forProfile profile :=
  ⟨rfl, rfl⟩

theorem folderEditLeavesOtherFolder (s : State) (folder : Folder) :
    (s.applyEdit (.setFolder folder)).folders.forProfile s.profile.other =
    s.folders.forProfile s.profile.other := by
  by_cases accepted : (s.folders.withFolder s.profile folder).separate = true
  · rw [State.applyEdit, State.tryEdit, ite_eq_left accepted]
    cases s.profile <;> rfl
  · simp [State.applyEdit, State.tryEdit, accepted]

theorem staysSeparate (s : State) (edits : List Edit) (valid : s.folders.separate = true) :
    (edits.foldl State.applyEdit s).folders.separate = true := by
  induction edits generalizing s with
  | nil => exact valid
  | cons edit rest ih =>
    simp only [List.foldl_cons]
    apply ih
    cases edit with
    | switch profile => exact valid
    | setFolder folder =>
      by_cases accepted : (s.folders.withFolder s.profile folder).separate = true
      · simp [State.applyEdit, State.tryEdit, accepted]
      · simpa [State.applyEdit, State.tryEdit, accepted] using valid

-- No canonical path can be equal to or beneath both configured folders.
theorem noSharedPath (s : State) (edits : List Edit) (valid : s.folders.separate = true)
    {regular ptr path : Path}
    (liveRoot : (edits.foldl State.applyEdit s).folders.regular = some regular)
    (ptrRoot : (edits.foldl State.applyEdit s).folders.ptr = some ptr) :
    ¬ (regular <+: path ∧ ptr <+: path) := by
  intro ⟨inRegular, inPTR⟩
  have separate := staysSeparate s edits valid
  unfold Folders.separate at separate
  rw [liveRoot, ptrRoot] at separate
  rcases List.prefix_or_prefix_of_prefix inRegular inPTR with nested | nested <;>
    simp [List.isPrefixOf_iff_prefix.mpr nested] at separate

private structure Case where
  before : Folders
  gameSlug : String
  selectedFolder : Folder
  proposedFolder : Folder
  after : Option Folders
  deriving ToJson

-- Export expected outcomes from the proved model; Go replays them on real directories.
def main : IO Unit := do
  let roots : List Folder := [none, some [], some ["game"], some ["game", "child"],
    some ["game-ptr"], some ["game-ptr", "child"]]
  let mut checks : Array Case := #[]
  for regular in roots do
    for ptr in roots do
      let before : Folders := ⟨regular, ptr⟩
      if !before.separate then continue
      for (profile, gameSlug) in [(Profile.regular, "roguelike-prod"), (Profile.ptr, "roguelike")] do
        let start : State := ⟨profile.other, before⟩
        let selected := start.applyEdit (.switch profile)
        for proposedFolder in roots do
          let after := (selected.tryEdit (.setFolder proposedFolder)).map State.folders
          checks := checks.push ⟨before, gameSlug, selected.activeFolder, proposedFolder, after⟩
  IO.println (toJson checks).compress

end ProfileIsolation

def main : IO Unit := ProfileIsolation.main
