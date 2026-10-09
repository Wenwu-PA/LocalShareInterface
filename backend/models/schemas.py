from pydantic import BaseModel, Field


class Credentials(BaseModel):
    username: str = Field(min_length=3, max_length=64, pattern=r"^[a-zA-Z0-9_.-]+$")
    password: str = Field(min_length=1, max_length=256)


class UploadStart(BaseModel):
    path: str = Field(min_length=1, max_length=1024)
    size: int = Field(ge=0)
    modified: int = Field(default=0, ge=0)


class FileAction(BaseModel):
    path: str = Field(min_length=1, max_length=1024)


class RenameAction(FileAction):
    new_name: str = Field(min_length=1, max_length=240)


class ShareCreate(BaseModel):
    path: str = Field(min_length=1, max_length=1024)
    ttl_hours: int = Field(default=24, ge=1, le=720)
    max_downloads: int = Field(default=10, ge=1, le=10000)
    password: str | None = Field(default=None, max_length=256)


class PasswordUnlock(BaseModel):
    password: str = Field(min_length=1, max_length=256)


class TunnelToggle(BaseModel):
    enabled: bool


class UserCreate(BaseModel):
    username: str = Field(min_length=3, max_length=64, pattern=r"^[a-zA-Z0-9_.-]+$")
    password: str = Field(min_length=12, max_length=256)


class PasswordChange(BaseModel):
    current_password: str = Field(min_length=1, max_length=256)
    new_password: str = Field(min_length=12, max_length=256)


class UploadPause(BaseModel):
    paused: bool
