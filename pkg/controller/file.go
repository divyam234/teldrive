package controller

import (
	"net/http"

	"github.com/divyam234/teldrive/internal/auth"
	"github.com/divyam234/teldrive/internal/cache"
	"github.com/divyam234/teldrive/internal/logging"
	"github.com/divyam234/teldrive/pkg/httputil"
	"github.com/divyam234/teldrive/pkg/schemas"
	"github.com/gin-gonic/gin"
)

func (fc *Controller) CreateFile(c *gin.Context) {

	var fileIn schemas.FileIn

	logger := logging.FromContext(c)

	if err := c.ShouldBindJSON(&fileIn); err != nil {
		logger.Error(err)
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	userId, _ := auth.GetUser(c)

	res, err := fc.FileService.CreateFile(c, userId, &fileIn)
	if err != nil {
		logger.Error(err)
		httputil.NewError(c, err.Code, err.Error)
		return
	}
	c.JSON(http.StatusCreated, res)
}

func (fc *Controller) UpdateFile(c *gin.Context) {

	userId, _ := auth.GetUser(c)

	var fileUpdate schemas.FileUpdate

	if err := c.ShouldBindJSON(&fileUpdate); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	res, err := fc.FileService.UpdateFile(c.Param("fileID"), userId, &fileUpdate, cache.FromContext(c))
	if err != nil {
		httputil.NewError(c, err.Code, err.Error)
		return
	}

	c.JSON(http.StatusOK, res)
}

func (fc *Controller) GetFileByID(c *gin.Context) {
	res, err := fc.FileService.GetFileByID(c.Param("fileID"))
	if err != nil {
		httputil.NewError(c, http.StatusNotFound, err.Error)
		return
	}

	c.JSON(http.StatusOK, res)
}

func (fc *Controller) ListFiles(c *gin.Context) {

	userId, _ := auth.GetUser(c)

	fquery := schemas.FileQuery{
		PerPage: 500,
		Order:   "asc",
		Sort:    "name",
		Op:      "list",
	}

	if err := c.ShouldBindQuery(&fquery); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	res, err := fc.FileService.ListFiles(userId, &fquery)
	if err != nil {
		httputil.NewError(c, err.Code, err.Error)
		return
	}

	c.JSON(http.StatusOK, res)
}

func (fc *Controller) MakeDirectory(c *gin.Context) {

	userId, _ := auth.GetUser(c)

	var payload schemas.MkDir
	if err := c.ShouldBindJSON(&payload); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	res, err := fc.FileService.MakeDirectory(userId, &payload)
	if err != nil {
		httputil.NewError(c, err.Code, err.Error)
		return
	}

	c.JSON(http.StatusOK, res)
}

func (fc *Controller) CopyFile(c *gin.Context) {
	res, err := fc.FileService.CopyFile(c)
	if err != nil {
		httputil.NewError(c, err.Code, err.Error)
		return
	}

	c.JSON(http.StatusOK, res)
}

func (fc *Controller) MoveFiles(c *gin.Context) {

	userId, _ := auth.GetUser(c)

	var payload schemas.FileOperation
	if err := c.ShouldBindJSON(&payload); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	res, err := fc.FileService.MoveFiles(userId, &payload)
	if err != nil {
		httputil.NewError(c, err.Code, err.Error)
		return
	}

	c.JSON(http.StatusOK, res)
}

func (fc *Controller) DeleteFiles(c *gin.Context) {

	userId, _ := auth.GetUser(c)

	var payload schemas.DeleteOperation
	if err := c.ShouldBindJSON(&payload); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	res, err := fc.FileService.DeleteFiles(userId, &payload)
	if err != nil {
		httputil.NewError(c, err.Code, err.Error)
		return
	}

	c.JSON(http.StatusOK, res)
}

func (fc *Controller) DeleteFileParts(c *gin.Context) {

	res, err := fc.FileService.DeleteFileParts(c, c.Param("fileID"))
	if err != nil {
		httputil.NewError(c, err.Code, err.Error)
		return
	}

	c.JSON(http.StatusOK, res)
}

func (fc *Controller) MoveDirectory(c *gin.Context) {
	userId, _ := auth.GetUser(c)

	var payload schemas.DirMove
	if err := c.ShouldBindJSON(&payload); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	res, err := fc.FileService.MoveDirectory(userId, &payload)
	if err != nil {
		httputil.NewError(c, err.Code, err.Error)
		return
	}

	c.JSON(http.StatusOK, res)
}

func (fc *Controller) GetCategoryStats(c *gin.Context) {
	userId, _ := auth.GetUser(c)

	res, err := fc.FileService.GetCategoryStats(userId)
	if err != nil {
		httputil.NewError(c, err.Code, err.Error)
		return
	}

	c.JSON(http.StatusOK, res)
}

func (fc *Controller) GetFileStream(c *gin.Context) {
	fc.FileService.GetFileStream(c, false)
}

func (fc *Controller) GetFileDownload(c *gin.Context) {
	fc.FileService.GetFileStream(c, true)
}
