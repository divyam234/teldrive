package controller

import (
	"net/http"
	"strconv"

	"github.com/divyam234/teldrive/internal/auth"
	"github.com/divyam234/teldrive/pkg/httputil"
	"github.com/gin-gonic/gin"
)

func (uc *Controller) GetUploadFileById(c *gin.Context) {
	res, err := uc.UploadService.GetUploadFileById(c)
	if err != nil {
		httputil.NewError(c, err.Code, err.Error)
		return
	}

	c.JSON(http.StatusOK, res)
}

func (uc *Controller) DeleteUploadFile(c *gin.Context) {
	res, err := uc.UploadService.DeleteUploadFile(c)
	if err != nil {
		httputil.NewError(c, err.Code, err.Error)
		return
	}

	c.JSON(http.StatusOK, res)
}

func (uc *Controller) UploadFile(c *gin.Context) {
	res, err := uc.UploadService.UploadFile(c)
	if err != nil {
		httputil.NewError(c, err.Code, err.Error)
		return
	}

	c.JSON(http.StatusCreated, res)
}

func (uc *Controller) UploadStats(c *gin.Context) {
	userId, _ := auth.GetUser(c)

	days := 7

	if c.Query("days") != "" {
		days, _ = strconv.Atoi(c.Query("days"))
	}

	res, err := uc.UploadService.GetUploadStats(userId, days)
	if err != nil {
		httputil.NewError(c, err.Code, err.Error)
		return
	}

	c.JSON(http.StatusCreated, res)
}
